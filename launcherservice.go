package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"arclights/installer"
	"arclights/networks"

	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// LauncherService exposes Gryphline/Endfield API and installer functionality to the application.
type LauncherService struct {
	client            *networks.LauncherClient
	httpClient        *http.Client
	app               *application.App
	mainWindow        *application.WebviewWindow
	updater           *updater.Updater
	mu                sync.Mutex
	activeCtx         context.Context
	cancelFunc        context.CancelFunc
	activeOperation   *launcherOperation
	isClearingCache   atomic.Bool
	isWatchingProcess bool
}

// launcherOperation tracks work which may modify the game directory. Keeping a
// completion signal lets destructive operations wait until cancelled workers
// have stopped using the directory.
type launcherOperation struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// LauncherSettings stores user-configured paths and preferences.
type LauncherSettings struct {
	InstallPath         string `json:"install_path"`
	CreateShortcut      bool   `json:"create_shortcut"`
	AutoLaunch          bool   `json:"auto_launch"`
	SilentUpdate        bool   `json:"silent_update"`
	CloseAction         string `json:"close_action"`
	SpeedLimitMode      string `json:"speed_limit_mode"`
	SpeedLimitKB        int    `json:"speed_limit_kb"`
	Language            string `json:"language"`
	UpdateChannel       string `json:"update_channel"`
	RenderingMode       string `json:"rendering_mode"`
	LaunchCommand       string `json:"launch_command"`
	TroubleshootingMode bool   `json:"troubleshooting_mode"`
}

// LauncherUpdateInfo represents the result of checking for launcher self-updates.
type LauncherUpdateInfo struct {
	HasUpdate      bool   `json:"has_update"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Name           string `json:"name"`
	Notes          string `json:"notes"`
}

// DiskSpaceInfo represents space on the target volume and download requirements.
type DiskSpaceInfo struct {
	RequiredBytes uint64  `json:"required_bytes"`
	FreeBytes     uint64  `json:"free_bytes"`
	TotalBytes    uint64  `json:"total_bytes"`
	RequiredGB    float64 `json:"required_gb"`
	FreeGB        float64 `json:"free_gb"`
}

// GameStateInfo represents the current installation and update status.
type GameStateInfo struct {
	Status         string         `json:"status"`
	InstallPath    string         `json:"install_path"`
	Version        string         `json:"version"`
	LatestVersion  string         `json:"latest_version"`
	DiskSpace      *DiskSpaceInfo `json:"disk_space"`
	CreateShortcut bool           `json:"create_shortcut"`
}

// NewLauncherService creates a new LauncherService with an optimized streaming HTTP transport.
func NewLauncherService() (*LauncherService, error) {
	client, err := networks.NewLauncherClient()
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ReadBufferSize:        512 * 1024,
		WriteBufferSize:       256 * 1024,
	}
	return &LauncherService{
		client: client,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   0,
		},
	}, nil
}

func (s *LauncherService) getSettingsPath() string {
	appData := os.Getenv("LOCALAPPDATA")
	if appData == "" {
		appData = os.TempDir()
	}
	dir := filepath.Join(appData, "Arclights")
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "settings.json")
}

func (s *LauncherService) SetApp(app *application.App) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.app = app
	if app != nil {
		s.updater = app.Updater
	}
}

// SetMainWindow attaches the primary application window to LauncherService.
func (s *LauncherService) SetMainWindow(window *application.WebviewWindow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mainWindow = window
}

// HideWindow hides the launcher window to the system tray.
func (s *LauncherService) HideWindow() {
	s.mu.Lock()
	w := s.mainWindow
	s.mu.Unlock()
	if w != nil {
		w.Hide()
	} else if app := application.Get(); app != nil {
		app.Hide()
	}
}

// ShowWindow restores the launcher window from the system tray and brings it to front.
func (s *LauncherService) ShowWindow() {
	s.mu.Lock()
	w := s.mainWindow
	s.mu.Unlock()
	if w != nil {
		if w.IsMinimised() {
			w.UnMinimise()
		}
		w.Show()
		w.Focus()
	} else if app := application.Get(); app != nil {
		app.Show()
	}
}

func (s *LauncherService) setWindowsAutoLaunch(enabled bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	if enabled {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return k.SetStringValue("Arclights", `"`+exe+`"`)
	}

	err = k.DeleteValue("Arclights")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

func (s *LauncherService) isWindowsAutoLaunchEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	val, _, err := k.GetStringValue("Arclights")
	return err == nil && val != ""
}

func (s *LauncherService) loadSettings() LauncherSettings {
	cfg := LauncherSettings{
		CreateShortcut:      true,
		CloseAction:         "exit",
		Language:            "English",
		SpeedLimitMode:      "none",
		SpeedLimitKB:        4096,
		UpdateChannel:       "release",
		RenderingMode:       "vk",
		LaunchCommand:       "",
		TroubleshootingMode: false,
	}
	data, err := os.ReadFile(s.getSettingsPath())
	if err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	if cfg.InstallPath == "" {
		cfg.InstallPath = s.getDefaultInstallPath()
	}
	if cfg.CloseAction == "" {
		cfg.CloseAction = "exit"
	}
	if cfg.Language == "" {
		cfg.Language = "English"
	}
	if cfg.SpeedLimitMode == "" {
		cfg.SpeedLimitMode = "none"
	}
	if cfg.SpeedLimitKB <= 0 {
		cfg.SpeedLimitKB = 4096
	}
	if cfg.UpdateChannel == "" {
		cfg.UpdateChannel = "release"
	}
	if cfg.RenderingMode == "" {
		cfg.RenderingMode = "vk"
	}
	cfg.AutoLaunch = s.isWindowsAutoLaunchEnabled()
	return cfg
}

func (s *LauncherService) GetLauncherSettings() LauncherSettings {
	return s.loadSettings()
}

func (s *LauncherService) SaveLauncherSettings(cfg LauncherSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing := s.loadSettings()
	if cfg.InstallPath == "" {
		cfg.InstallPath = existing.InstallPath
	}
	if cfg.RenderingMode == "" {
		cfg.RenderingMode = existing.RenderingMode
	}
	if cfg.Language == "" {
		cfg.Language = existing.Language
	}
	if cfg.UpdateChannel == "" {
		cfg.UpdateChannel = existing.UpdateChannel
	}

	_ = s.setWindowsAutoLaunch(cfg.AutoLaunch)
	s.saveSettings(cfg)
	return nil
}

func (s *LauncherService) buildLaunchArguments(customArgs string) string {
	settings := s.loadSettings()
	var parts []string

	hasDX11 := strings.Contains(customArgs, "-dx11") || strings.Contains(settings.LaunchCommand, "-dx11")
	if settings.RenderingMode == "dx" && !hasDX11 {
		parts = append(parts, "-dx11")
	}

	trimmedCmd := strings.TrimSpace(settings.LaunchCommand)
	if trimmedCmd != "" {
		parts = append(parts, trimmedCmd)
	}

	trimmedCustom := strings.TrimSpace(customArgs)
	if trimmedCustom != "" && trimmedCustom != trimmedCmd {
		parts = append(parts, trimmedCustom)
	}

	return strings.Join(parts, " ")
}

func (s *LauncherService) CheckLauncherUpdate() (*LauncherUpdateInfo, error) {
	s.mu.Lock()
	up := s.updater
	s.mu.Unlock()

	curVer := "1.6.0"
	if up != nil && up.CurrentVersion() != "" {
		curVer = up.CurrentVersion()
	}

	info := &LauncherUpdateInfo{
		HasUpdate:      false,
		CurrentVersion: curVer,
		LatestVersion:  curVer,
	}

	if up == nil {
		return info, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	rel, err := up.Check(ctx)
	if err != nil {
		return nil, fmt.Errorf("check for updates: %w", err)
	}
	if rel == nil {
		return info, nil
	}

	info.HasUpdate = true
	info.LatestVersion = rel.Version
	info.Name = rel.Name
	info.Notes = rel.Notes
	return info, nil
}

func (s *LauncherService) InstallLauncherUpdate() error {
	s.mu.Lock()
	up := s.updater
	s.mu.Unlock()

	if up == nil {
		return errors.New("updater is not initialized")
	}

	return up.DownloadAndInstall(context.Background())
}

func (s *LauncherService) RestartLauncher() error {
	s.mu.Lock()
	up := s.updater
	s.mu.Unlock()

	if up == nil {
		return errors.New("updater is not initialized")
	}

	return up.Restart(context.Background())
}

func (s *LauncherService) OpenLogsFolder() error {
	appData := os.Getenv("LOCALAPPDATA")
	if appData == "" {
		appData = os.TempDir()
	}
	logDir := filepath.Join(appData, "arclights", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	return exec.Command("explorer.exe", logDir).Start()
}

func (s *LauncherService) OpenDirectory(path string) error {
	if path == "" {
		cfg := s.loadSettings()
		path = cfg.InstallPath
	}
	_ = os.MkdirAll(path, 0o755)
	return exec.Command("explorer.exe", path).Start()
}

func (s *LauncherService) saveSettings(cfg LauncherSettings) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err == nil {
		_ = os.WriteFile(s.getSettingsPath(), data, 0o644)
	}
}

func (s *LauncherService) getDefaultInstallPath() string {
	drives := []string{`E:\`, `D:\`, `C:\`}
	bestDrive := `C:\`
	var maxFree uint64

	for _, d := range drives {
		var free, total, totalFree uint64
		err := windows.GetDiskFreeSpaceEx(windows.StringToUTF16Ptr(d), &free, &total, &totalFree)
		if err == nil && free > maxFree {
			maxFree = free
			bestDrive = d
		}
	}
	return filepath.Join(bestDrive, "Arknights Endfield")
}

// GetDiskSpace returns space details for the volume containing dirPath.
func (s *LauncherService) GetDiskSpace(dirPath string) (*DiskSpaceInfo, error) {
	if dirPath == "" {
		settings := s.loadSettings()
		dirPath = settings.InstallPath
	}
	vol := filepath.VolumeName(dirPath)
	if vol == "" {
		vol = `C:\`
	} else if !strings.HasSuffix(vol, `\`) {
		vol += `\`
	}

	var free, total, totalFree uint64
	err := windows.GetDiskFreeSpaceEx(windows.StringToUTF16Ptr(vol), &free, &total, &totalFree)
	if err != nil {
		return nil, fmt.Errorf("failed to get disk space for %s: %w", vol, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var requiredBytes uint64
	rsp, err := s.client.GetLatestGame(ctx, "")
	if err == nil && rsp != nil {
		up := rsp.Update()
		if up.Pkg != nil {
			for _, p := range up.Pkg.Packs {
				requiredBytes += p.PackageSize.Uint64()
			}
		}
	}

	return &DiskSpaceInfo{
		RequiredBytes: requiredBytes,
		FreeBytes:     free,
		TotalBytes:    total,
		RequiredGB:    float64(requiredBytes) / (1024 * 1024 * 1024),
		FreeGB:        float64(free) / (1024 * 1024 * 1024),
	}, nil
}

func (s *LauncherService) isGameInstalled(installPath string, cfg *networks.LocalGameConfig) bool {
	if installPath == "" {
		return false
	}
	exeName := "Endfield.exe"
	if cfg != nil && cfg.Entry != "" {
		exeName = cfg.Entry
	}
	exePath := filepath.Join(installPath, exeName)
	fi, err := os.Stat(exePath)
	return err == nil && !fi.IsDir()
}

func isProcessRunning(processName string) bool {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snapshot)

	var procEntry windows.ProcessEntry32
	procEntry.Size = uint32(unsafe.Sizeof(procEntry))

	if err := windows.Process32First(snapshot, &procEntry); err != nil {
		return false
	}

	target := strings.ToLower(processName)
	for {
		name := windows.UTF16ToString(procEntry.ExeFile[:])
		if strings.ToLower(name) == target {
			return true
		}
		if err := windows.Process32Next(snapshot, &procEntry); err != nil {
			break
		}
	}
	return false
}

func killProcessByPID(pid int) {
	if pid <= 0 {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	_ = windows.TerminateProcess(h, 0)
}

func (s *LauncherService) watchGameProcess(exeName string, launcherPID int) {
	s.mu.Lock()
	if s.isWatchingProcess {
		s.mu.Unlock()
		return
	}
	s.isWatchingProcess = true
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			s.isWatchingProcess = false
			s.mu.Unlock()
		}()

		// Give the game process a few moments to appear in the OS process list
		started := false
		for i := 0; i < 10; i++ {
			if isProcessRunning(exeName) {
				started = true
				break
			}
			time.Sleep(500 * time.Millisecond)
		}

		if started {
			for isProcessRunning(exeName) {
				time.Sleep(1000 * time.Millisecond)
			}
		}

		// When game finishes, kill our Games.exe by PID if it is still alive
		killProcessByPID(launcherPID)

		if app := application.Get(); app != nil {
			app.Event.Emit("installer:status:change", "ready")
		}

		// Restore launcher window from system tray when the game exits
		s.ShowWindow()
	}()
}

// GetGameState checks installation status, version, and volume disk space.
func (s *LauncherService) GetGameState() (*GameStateInfo, error) {
	settings := s.loadSettings()
	diskSpace, _ := s.GetDiskSpace(settings.InstallPath)

	info := &GameStateInfo{
		Status:         "not_installed",
		InstallPath:    settings.InstallPath,
		DiskSpace:      diskSpace,
		CreateShortcut: settings.CreateShortcut,
	}

	s.mu.Lock()
	isActive := s.activeCtx != nil
	s.mu.Unlock()
	if isActive {
		info.Status = "downloading"
		return info, nil
	}

	cfg, err := s.DetectInstalledConfig(settings.InstallPath)
	gameInstalled := s.isGameInstalled(settings.InstallPath, cfg)
	if err == nil && cfg != nil && cfg.Version != "" && gameInstalled {
		exeName := "Endfield.exe"
		if cfg.Entry != "" {
			exeName = cfg.Entry
		}
		if isProcessRunning(exeName) {
			info.Version = cfg.Version
			info.LatestVersion = cfg.Version
			info.Status = "running"
			s.watchGameProcess(exeName, 0)
			return info, nil
		}

		info.Version = cfg.Version
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rsp, err := s.client.GetLatestGame(ctx, cfg.Version)
		if err == nil && rsp != nil {
			info.LatestVersion = rsp.Version
			up := rsp.Update()
			if up.Type == networks.UpdateTypeFull || up.Type == networks.UpdateTypePatch {
				info.Status = "update_available"
			} else {
				info.Status = "ready"
			}
		} else {
			info.Status = "ready"
		}
	} else {
		progressPath := filepath.Join(settings.InstallPath, ".arclights.progress.json")
		if _, err := os.Stat(progressPath); err == nil {
			info.Status = "paused"
		} else {
			info.Status = "not_installed"
		}
	}

	return info, nil
}

// SelectInstallDirectory opens a native directory picker dialog.
func (s *LauncherService) SelectInstallDirectory() (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("app not initialized")
	}
	dlg := app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		SetTitle("Select Game Installation Directory")
	path, err := dlg.PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	if path != "" {
		path = filepath.Clean(path)
	}
	return path, nil
}

// StartInstall initiates background streaming installation directly to installPath.
func (s *LauncherService) StartInstall(installPath string, createShortcut bool) error {
	if installPath == "" {
		return errors.New("install path cannot be empty")
	}
	op, err := s.beginOperation()
	if err != nil {
		return err
	}

	currentSettings := s.loadSettings()
	currentSettings.InstallPath = installPath
	currentSettings.CreateShortcut = createShortcut
	s.saveSettings(currentSettings)

	go func() {
		defer s.finishOperation(op)
		app := application.Get()
		if app != nil {
			app.Event.Emit("installer:status:change", "downloading")
		}

		err := s.streamInstallGame(op.ctx, installPath)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				if s.isClearingCache.Load() {
					return
				}
				if app != nil {
					app.Event.Emit("installer:status:change", "paused")
				}
				return
			}
			if s.isClearingCache.Load() {
				return
			}
			if app != nil {
				app.Event.Emit("installer:error", err.Error())
				app.Event.Emit("installer:status:change", "error")
			}
			return
		}

		if createShortcut {
			_ = s.CreateDesktopShortcut(installPath)
		}

		if app != nil {
			app.Event.Emit("installer:status:change", "ready")
		}
	}()

	return nil
}

// StartUpdate applies delta patches or full updates in a background goroutine.
func (s *LauncherService) StartUpdate(installPath string) error {
	cfg, err := s.DetectInstalledConfig(installPath)
	if err != nil {
		return err
	}
	op, err := s.beginOperation()
	if err != nil {
		return err
	}

	go func() {
		defer s.finishOperation(op)
		app := application.Get()
		if app != nil {
			app.Event.Emit("installer:status:change", "downloading")
		}

		err := s.streamUpdateGame(op.ctx, installPath, cfg.Version)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				if s.isClearingCache.Load() {
					return
				}
				if app != nil {
					app.Event.Emit("installer:status:change", "paused")
				}
				return
			}
			if s.isClearingCache.Load() {
				return
			}
			if app != nil {
				app.Event.Emit("installer:error", err.Error())
				app.Event.Emit("installer:status:change", "error")
			}
			return
		}

		if app != nil {
			app.Event.Emit("installer:status:change", "ready")
		}
	}()

	return nil
}

// startEndfieldProcess starts the game process with Games.exe as its parent in a suspended state.
func (s *LauncherService) startEndfieldProcess(installPath string, arguments string) (gameCmd *exec.Cmd, launcherCmd *exec.Cmd, launcherPID int, exeName string, err error) {
	if installPath == "" {
		settings := s.loadSettings()
		installPath = settings.InstallPath
	}

	exeName = "Endfield.exe"
	if cfg, errDetect := s.DetectInstalledConfig(installPath); errDetect == nil && cfg != nil && cfg.Entry != "" {
		exeName = cfg.Entry
	}
	exePath := filepath.Join(installPath, exeName)
	if _, errStat := os.Stat(exePath); errStat != nil {
		return nil, nil, 0, exeName, fmt.Errorf("game executable %q not found: %w", exePath, errStat)
	}

	runtimeDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "arclights", "runtime")
	_ = os.MkdirAll(runtimeDir, 0o755)
	launcher := filepath.Join(runtimeDir, "Games.exe")
	if _, errl := os.Stat(launcher); os.IsNotExist(errl) {
		client, errClient := networks.NewLauncherClient()
		if errClient != nil {
			return nil, nil, 0, exeName, errClient
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		obj, errf := client.GetLatestLauncher(ctx, "")
		if errf != nil {
			return nil, nil, 0, exeName, errf
		}
		if obj.ZipPackageUrl == "" {
			return nil, nil, 0, exeName, errors.New("launcher package URL is empty")
		}
		if errExtract := installer.ExtractFile(obj.ZipPackageUrl, "Games.exe", launcher); errExtract != nil {
			return nil, nil, 0, exeName, fmt.Errorf("failed to extract Games.exe: %w", errExtract)
		}
	}

	launcherCmd = exec.Command(launcher)
	launcherCmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW,
	}

	if errStart := launcherCmd.Start(); errStart != nil {
		return nil, nil, 0, exeName, fmt.Errorf("failed to start Games.exe: %w", errStart)
	}

	launcherPID = launcherCmd.Process.Pid
	launcherHandle, errHandle := windows.OpenProcess(
		windows.PROCESS_CREATE_PROCESS|windows.PROCESS_DUP_HANDLE|windows.PROCESS_QUERY_INFORMATION,
		false,
		uint32(launcherPID),
	)
	if errHandle != nil {
		_ = launcherCmd.Process.Kill()
		killProcessByPID(launcherPID)
		return nil, nil, 0, exeName, fmt.Errorf("failed to open Games.exe process handle: %w", errHandle)
	}
	defer windows.CloseHandle(launcherHandle)

	finalArgs := s.buildLaunchArguments(arguments)
	if finalArgs != "" {
		gameCmd = exec.Command(exePath, strings.Fields(finalArgs)...)
	} else {
		gameCmd = exec.Command(exePath)
	}
	gameCmd.Dir = installPath
	gameCmd.SysProcAttr = &syscall.SysProcAttr{
		ParentProcess: syscall.Handle(launcherHandle),
	}

	if errGameStart := gameCmd.Start(); errGameStart != nil {
		_ = launcherCmd.Process.Kill()
		killProcessByPID(launcherPID)
		return nil, nil, 0, exeName, fmt.Errorf("failed to start %s: %w", exeName, errGameStart)
	}

	return gameCmd, launcherCmd, launcherPID, exeName, nil
}

// LaunchGame starts the game executable and monitors its process.
func (s *LauncherService) LaunchGame(installPath string, arguments string) error {
	app := application.Get()
	if app != nil {
		app.Event.Emit("installer:status:change", "booting")
	}

	_, launcherCmd, launcherPID, exeName, err := s.startEndfieldProcess(installPath, arguments)
	if err != nil {
		if app != nil {
			app.Event.Emit("installer:status:change", "ready")
		}
		return err
	}

	if app != nil {
		app.Event.Emit("installer:status:change", "running")
	}

	// Kill our Games.exe after 60 seconds of game execution
	go func() {
		time.Sleep(60 * time.Second)
		if launcherCmd != nil && launcherCmd.Process != nil {
			_ = launcherCmd.Process.Kill()
		}
		killProcessByPID(launcherPID)
	}()

	// Hide launcher to system tray shortly after starting the game
	go func() {
		time.Sleep(500 * time.Millisecond)
		s.HideWindow()
	}()

	s.watchGameProcess(exeName, launcherPID)
	return nil
}

// CreateDesktopShortcut creates a Windows desktop shortcut pointing to the launcher with --start.
func (s *LauncherService) CreateDesktopShortcut(installPath string) error {
	if installPath == "" {
		settings := s.loadSettings()
		installPath = settings.InstallPath
	}

	exeName := "Endfield.exe"
	if cfg, err := s.DetectInstalledConfig(installPath); err == nil && cfg != nil && cfg.Entry != "" {
		exeName = cfg.Entry
	}
	gameExePath := filepath.Join(installPath, exeName)

	launcherExe, err := os.Executable()
	if err != nil {
		return err
	}
	launcherExe = filepath.Clean(launcherExe)

	shortcutPath, err := desktopShortcutPath()
	if err != nil {
		return err
	}

	psScript := fmt.Sprintf(`
$WshShell = New-Object -comObject WScript.Shell
$Shortcut = $WshShell.CreateShortcut(%s)
$Shortcut.TargetPath = %s
$Shortcut.Arguments = "--start"
$Shortcut.WorkingDirectory = %s
$Shortcut.IconLocation = %s
$Shortcut.Save()
`, powerShellSingleQuoted(shortcutPath), powerShellSingleQuoted(launcherExe), powerShellSingleQuoted(filepath.Dir(launcherExe)), powerShellSingleQuoted(gameExePath+",0"))

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
	return cmd.Run()
}

// powerShellSingleQuoted returns a PowerShell string literal without allowing
// variable or backtick expansion in a filesystem path.
func powerShellSingleQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// desktopShortcutPath uses the Windows known Desktop folder so shortcut
// creation and deletion work when the desktop has been redirected.
func desktopShortcutPath() (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "[System.Environment]::GetFolderPath('Desktop')")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolve desktop directory: %w", err)
	}
	desktopDir := strings.TrimSpace(string(output))
	if desktopDir == "" {
		return "", errors.New("desktop directory is empty")
	}
	return filepath.Join(desktopDir, "Arknights Endfield.lnk"), nil
}

// OpenGameDirectory opens the install directory in Windows File Explorer.
func (s *LauncherService) OpenGameDirectory(installPath string) error {
	if installPath == "" {
		settings := s.loadSettings()
		installPath = settings.InstallPath
	}
	if _, err := os.Stat(installPath); err != nil {
		_ = os.MkdirAll(installPath, 0o755)
	}
	return exec.Command("explorer.exe", filepath.Clean(installPath)).Start()
}

// ClearDownloadCache cancels active download operations and removes all temporary/cached download data.
func (s *LauncherService) ClearDownloadCache(installPath string) error {
	s.isClearingCache.Store(true)
	defer s.isClearingCache.Store(false)

	s.cancelOperationAndWait()
	op, err := s.beginOperation()
	if err != nil {
		return err
	}
	defer s.finishOperation(op)

	if installPath == "" {
		settings := s.loadSettings()
		installPath = settings.InstallPath
	}
	if installPath == "" {
		return nil
	}

	cleanPath := filepath.Clean(installPath)
	vol := filepath.VolumeName(cleanPath)
	if cleanPath == vol || cleanPath == vol+"\\" || cleanPath == vol+"/" || cleanPath == "/" {
		return errors.New("cannot clear root directory")
	}

	cfg, _ := s.DetectInstalledConfig(cleanPath)
	gameInstalled := s.isGameInstalled(cleanPath, cfg)

	if !gameInstalled {
		_ = os.Remove(filepath.Join(cleanPath, ".arclights.progress.json"))
		_ = os.Remove(filepath.Join(cleanPath, ".arclights.state.json"))
		_ = os.Remove(filepath.Join(cleanPath, "config.ini"))

		entries, err := os.ReadDir(cleanPath)
		if err == nil {
			for _, entry := range entries {
				itemPath := filepath.Join(cleanPath, entry.Name())
				_ = os.RemoveAll(itemPath)
			}
		}
	} else {
		_ = os.Remove(filepath.Join(cleanPath, ".arclights.progress.json"))
		_ = os.Remove(filepath.Join(cleanPath, ".arclights.state.json"))

		_ = filepath.WalkDir(cleanPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				name := d.Name()
				if strings.HasSuffix(name, ".arclights.tmp") || strings.HasSuffix(name, ".tmp") {
					_ = os.Remove(path)
				}
			}
			return nil
		})
	}

	app := application.Get()
	if app != nil {
		if !gameInstalled {
			app.Event.Emit("installer:status:change", "not_installed")
		}
		app.Event.Emit("installer:stream:progress", installer.StreamProgress{
			DownloadedBytes:    0,
			TotalDownloadBytes: 0,
			CompletedFiles:     0,
			CurrentFile:        "",
			SpeedBps:           0,
			Percent:            0,
			IsPaused:           false,
		})
	}

	return nil
}

// UninstallGame cancels active operations and completely deletes all game files from the install directory.
func (s *LauncherService) UninstallGame(installPath string) error {
	s.isClearingCache.Store(true)
	defer s.isClearingCache.Store(false)

	s.cancelOperationAndWait()
	op, err := s.beginOperation()
	if err != nil {
		return err
	}
	defer s.finishOperation(op)

	if installPath == "" {
		settings := s.loadSettings()
		installPath = settings.InstallPath
	}
	if installPath == "" {
		return nil
	}

	cleanPath := filepath.Clean(installPath)
	vol := filepath.VolumeName(cleanPath)
	if cleanPath == vol || cleanPath == vol+"\\" || cleanPath == vol+"/" || cleanPath == "/" {
		return errors.New("cannot uninstall from root directory")
	}

	cfg, err := s.DetectInstalledConfig(cleanPath)
	if err != nil {
		return fmt.Errorf("cannot uninstall: installed game configuration is unavailable: %w", err)
	}
	if !s.isGameInstalled(cleanPath, cfg) {
		return errors.New("cannot uninstall: game executable was not found")
	}

	entries, err := os.ReadDir(cleanPath)
	if err != nil {
		return fmt.Errorf("read game directory: %w", err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(cleanPath, entry.Name())); err != nil {
			return fmt.Errorf("remove game entry %q: %w", entry.Name(), err)
		}
	}

	shortcutPath, err := desktopShortcutPath()
	if err != nil {
		return err
	}
	if err := os.Remove(shortcutPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove desktop shortcut: %w", err)
	}

	app := application.Get()
	if app != nil {
		app.Event.Emit("installer:status:change", "not_installed")
		app.Event.Emit("installer:stream:progress", installer.StreamProgress{
			DownloadedBytes:    0,
			TotalDownloadBytes: 0,
			CompletedFiles:     0,
			CurrentFile:        "",
			SpeedBps:           0,
			Percent:            0,
			IsPaused:           false,
		})
	}

	return nil
}

// StartVerification runs game verification and repairs any corrupted files in the background.
func (s *LauncherService) StartVerification(installPath string) error {
	op, err := s.beginOperation()
	if err != nil {
		return err
	}

	go func() {
		defer s.finishOperation(op)
		app := application.Get()
		if app != nil {
			app.Event.Emit("installer:status:change", "verifying")
		}

		diffs, err := s.verifyGame(op.ctx, installPath)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				if s.isClearingCache.Load() {
					return
				}
				if app != nil {
					app.Event.Emit("installer:status:change", "paused")
				}
				return
			}
			if app != nil {
				app.Event.Emit("installer:error", err.Error())
				app.Event.Emit("installer:status:change", "ready")
			}
			return
		}

		if len(diffs) > 0 {
			_ = s.repairGame(op.ctx, installPath, diffs)
		}

		if app != nil {
			app.Event.Emit("installer:status:change", "ready")
		}
	}()
	return nil
}

// CheckUpdate checks for game updates for Arknights: Endfield.
// currentVersion = "" for a full clean install check.
func (s *LauncherService) CheckUpdate(currentVersion string) (*networks.GameRsp, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	return s.client.GetLatestGame(ctx, currentVersion)
}

// GetAnnouncements fetches current game news, notices, and events.
func (s *LauncherService) GetAnnouncements() (*networks.AnnouncementsRsp, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req := networks.NewEndfieldWebCommonReq()
	return s.client.GetAnnouncements(ctx, &req)
}

// GetBanners fetches carousel promotional banners with image URLs.
func (s *LauncherService) GetBanners() (*networks.BannersRsp, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req := networks.NewEndfieldWebCommonReq()
	return s.client.GetBanners(ctx, &req)
}

// GetLauncherMedia fetches all media (banners, background art/video, news, social links) in a single request.
func (s *LauncherService) GetLauncherMedia() (*networks.LauncherMediaBundle, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req := networks.NewEndfieldWebCommonReq()
	return s.client.GetLauncherMedia(ctx, &req)
}

// DetectInstalledConfig reads config.ini from a local game directory.
func (s *LauncherService) DetectInstalledConfig(installDir string) (*networks.LocalGameConfig, error) {
	cfg, err := networks.ReadInstalledGameConfig(installDir)
	if err != nil {
		return nil, fmt.Errorf("failed to detect installed game: %w", err)
	}
	return cfg, nil
}

// beginOperation reserves the game directory for one modifying operation.
func (s *LauncherService) beginOperation() (*launcherOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeOperation != nil {
		return nil, errors.New("another game operation is already active")
	}
	ctx, cancel := context.WithCancel(context.Background())
	op := &launcherOperation{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	s.activeOperation = op
	s.activeCtx = ctx
	s.cancelFunc = cancel
	return op, nil
}

func (s *LauncherService) finishOperation(op *launcherOperation) {
	s.mu.Lock()
	if s.activeOperation == op {
		s.activeOperation = nil
		s.activeCtx = nil
		s.cancelFunc = nil
		close(op.done)
	}
	s.mu.Unlock()
}

// CancelOperation signals an active verify, repair, or stream operation to stop.
func (s *LauncherService) CancelOperation() {
	s.mu.Lock()
	op := s.activeOperation
	s.mu.Unlock()
	if op != nil {
		op.cancel()
	}
}

func (s *LauncherService) cancelOperationAndWait() {
	s.mu.Lock()
	op := s.activeOperation
	s.mu.Unlock()
	if op == nil {
		return
	}
	op.cancel()
	<-op.done
}

// VerifyGame scans installDir against the live game_files CDN manifest and returns missing/corrupt files.
func (s *LauncherService) VerifyGame(installDir string) ([]installer.GameFileEntry, error) {
	op, err := s.beginOperation()
	if err != nil {
		return nil, err
	}
	defer s.finishOperation(op)
	return s.verifyGame(op.ctx, installDir)
}

func (s *LauncherService) verifyGame(ctx context.Context, installDir string) ([]installer.GameFileEntry, error) {
	rsp, err := s.client.GetLatestGame(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch game endpoints: %w", err)
	}
	up := rsp.Update()
	if up.Pkg == nil || up.Pkg.FilePath == "" {
		return nil, errors.New("game files CDN endpoint not found in API response")
	}

	manifest, err := installer.FetchManifest(ctx, s.httpClient, up.Pkg.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch manifest: %w", err)
	}

	app := application.Get()
	diffs, err := installer.VerifyGameFiles(ctx, installDir, manifest, 0, func(p installer.VerifyProgress) {
		if app != nil {
			app.Event.Emit("installer:verify:progress", p)
		}
	})
	if err != nil {
		return nil, err
	}

	return diffs, nil
}

// RepairGame downloads and restores the provided diff files directly from the loose files CDN.
func (s *LauncherService) RepairGame(installDir string, diffs []installer.GameFileEntry) error {
	if len(diffs) == 0 {
		return nil
	}
	op, err := s.beginOperation()
	if err != nil {
		return err
	}
	defer s.finishOperation(op)
	return s.repairGame(op.ctx, installDir, diffs)
}

func (s *LauncherService) repairGame(ctx context.Context, installDir string, diffs []installer.GameFileEntry) error {
	rsp, err := s.client.GetLatestGame(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to fetch game endpoints: %w", err)
	}
	up := rsp.Update()
	if up.Pkg == nil || up.Pkg.FilePath == "" {
		return errors.New("game files CDN endpoint not found in API response")
	}

	app := application.Get()
	return installer.RepairGameFiles(ctx, s.httpClient, up.Pkg.FilePath, installDir, diffs, 4, func(p installer.RepairProgress) {
		if app != nil {
			app.Event.Emit("installer:repair:progress", p)
		}
	})
}

// StreamInstallGame downloads all game files concurrently directly from the CDN manifest.
func (s *LauncherService) StreamInstallGame(destDir string) error {
	op, err := s.beginOperation()
	if err != nil {
		return err
	}
	defer s.finishOperation(op)
	return s.streamInstallGame(op.ctx, destDir)
}

func (s *LauncherService) streamInstallGame(ctx context.Context, destDir string) error {
	rsp, err := s.client.GetLatestGame(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to fetch latest game info: %w", err)
	}
	up := rsp.Update()

	if up.Pkg == nil || up.Pkg.FilePath == "" {
		return errors.New("game files CDN endpoint not found in API response")
	}

	manifest, err := installer.FetchManifest(ctx, s.httpClient, up.Pkg.FilePath)
	if err != nil {
		return fmt.Errorf("failed to fetch manifest: %w", err)
	}

	app := application.Get()
	settings := s.loadSettings()
	var speedLimitBytes int64
	if settings.SpeedLimitMode == "limit" && settings.SpeedLimitKB > 0 {
		speedLimitBytes = int64(settings.SpeedLimitKB) * 1024
	}

	err = installer.InstallManifestFiles(ctx, s.httpClient, up.Pkg.FilePath, destDir, manifest, 10, speedLimitBytes, func(p installer.StreamProgress) {
		if app != nil {
			app.Event.Emit("installer:stream:progress", p)
		}
	})
	if err != nil {
		return err
	}

	configPath := filepath.Join(destDir, "config.ini")
	if _, statErr := os.Stat(configPath); os.IsNotExist(statErr) {
		content := fmt.Sprintf("[Game]\nversion=%s\nentry=Endfield.exe\nappcode=endfield\nchannel=default\nsub_channel=default\nregion=en-us\n", rsp.Version)
		_ = os.WriteFile(configPath, []byte(content), 0644)
	}

	return nil
}

// StreamUpdateGame updates an existing game directory using a delta patch archive without saving the zip archive to disk.
func (s *LauncherService) StreamUpdateGame(installDir string, currentVersion string) error {
	op, err := s.beginOperation()
	if err != nil {
		return err
	}
	defer s.finishOperation(op)
	return s.streamUpdateGame(op.ctx, installDir, currentVersion)
}

func (s *LauncherService) streamUpdateGame(ctx context.Context, installDir string, currentVersion string) error {
	rsp, err := s.client.GetLatestGame(ctx, currentVersion)
	if err != nil {
		return fmt.Errorf("failed to check update: %w", err)
	}
	up := rsp.Update()
	if up.Type == networks.UpdateTypeUpToDate {
		return nil
	}
	if up.Type == networks.UpdateTypeFull {
		return s.streamInstallGame(ctx, installDir)
	}
	if up.Patch == nil || up.Patch.URL == "" {
		return errors.New("no patch package URL available")
	}

	app := application.Get()
	return installer.StreamApplyPatch(ctx, s.httpClient, up.Patch.URL, installDir, func(p installer.StreamProgress) {
		if app != nil {
			app.Event.Emit("installer:stream:progress", p)
		}
	})
}
