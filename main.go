package main

import (
	"embed"
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"
	"golang.org/x/sys/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed keys/releases.key.pub
var releasesPubKey []byte

//go:embed keys/nightly.key.pub
var nightlyPubKey []byte

//go:embed build/windows/icon.ico
var appIcon []byte

func init() {
	application.RegisterEvent[string]("time")
}

func showMessageBox(title, message string, boxType uint32) {
	tPtr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	mPtr, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return
	}
	_, _ = windows.MessageBox(0, mPtr, tPtr, boxType)
}

func runStartMode(s *LauncherService) {
	settings := s.loadSettings()
	installPath := settings.InstallPath
	cfg, err := s.DetectInstalledConfig(installPath)
	if err != nil || !s.isGameInstalled(installPath, cfg) {
		msg := fmt.Sprintf("Arknights: Endfield was not found at:\n%s\n\nPlease launch Arclights to download and install the game first.", installPath)
		showMessageBox("Arknights: Endfield", msg, windows.MB_OK|windows.MB_ICONWARNING|windows.MB_TOPMOST|windows.MB_SETFOREGROUND)
		os.Exit(0)
	}

	_, launcherCmd, launcherPID, exeName, err := s.startEndfieldProcess(installPath, "")
	if err != nil {
		msg := fmt.Sprintf("Failed to launch Arknights: Endfield:\n%v", err)
		showMessageBox("Arclights Error", msg, windows.MB_OK|windows.MB_ICONERROR|windows.MB_TOPMOST|windows.MB_SETFOREGROUND)
		os.Exit(1)
	}

	deadline := time.After(60 * time.Second)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			if launcherCmd != nil && launcherCmd.Process != nil {
				_ = launcherCmd.Process.Kill()
			}
			killProcessByPID(launcherPID)
			os.Exit(0)
		case <-ticker.C:
			if !isProcessRunning(exeName) {
				if launcherCmd != nil && launcherCmd.Process != nil {
					_ = launcherCmd.Process.Kill()
				}
				killProcessByPID(launcherPID)
				os.Exit(0)
			}
		}
	}
}

func main() {
	launcherSvc, err := NewLauncherService()
	if err != nil {
		log.Fatalf("failed to initialize launcher service: %v", err)
	}

	for _, arg := range os.Args[1:] {
		if arg == "--start" || arg == "-start" {
			runStartMode(launcherSvc)
			return
		}
	}

	dataDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "arclights", "WebView2")
	_ = os.MkdirAll(dataDir, 0o755)

	app := application.New(application.Options{
		Name:        "arclights",
		Description: "Arknights: Endfield alternative launcher",
		Icon:        appIcon,
		Services: []application.Service{
			application.NewService(launcherSvc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Windows: application.WindowsOptions{
			WebviewUserDataPath: dataDir,
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	settings := launcherSvc.GetLauncherSettings()
	pubKey := releasesPubKey
	manifestURL := "https://github.com/corruptedkey/Arclights/releases/latest/download/manifest.json"
	channel := "stable"
	if settings.UpdateChannel == "nightly" {
		pubKey = nightlyPubKey
		manifestURL = "https://github.com/corruptedkey/Arclights/releases/download/nightly/manifest.json"
		channel = "nightly"
	}

	epProvider, err := endpoint.New(endpoint.Config{
		URL:     manifestURL,
		Channel: channel,
	})
	if err == nil {
		var checkInterval time.Duration
		if settings.SilentUpdate {
			checkInterval = 4 * time.Hour
		}
		_ = app.Updater.Init(updater.Config{
			CurrentVersion: "0.0.1",
			Providers:      []updater.Provider{epProvider},
			PublicKey:      pubKey,
			CheckInterval:  checkInterval,
			Window:         updater.WindowNone,
		})
	}

	launcherSvc.SetApp(app)

	mainWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:              "Arclights",
		Width:              1280,
		Height:             760,
		ZoomControlEnabled: false,
		Windows: application.WindowsWindow{
			DisableFramelessWindowDecorations: true,
		},
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		Frameless:      true,
		BackgroundType: application.BackgroundTypeTransparent,
		URL:            "/",
	})

	launcherSvc.SetMainWindow(mainWindow)

	showMainScreen := func() {
		launcherSvc.ShowWindow()
	}

	tray := app.SystemTray.New()
	tray.SetIcon(appIcon)
	tray.SetTooltip("Arclights")

	trayMenu := application.NewMenu()
	trayMenu.Add("Display main screen").OnClick(func(ctx *application.Context) {
		showMainScreen()
	})
	trayMenu.Add("Exit").OnClick(func(ctx *application.Context) {
		app.Quit()
	})
	tray.SetMenu(trayMenu)

	tray.OnClick(func() {
		showMainScreen()
	})
	tray.OnDoubleClick(func() {
		showMainScreen()
	})

	go func() {
		for {
			now := time.Now().Format(time.RFC1123)
			app.Event.Emit("time", now)
			time.Sleep(time.Second)
		}
	}()

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
