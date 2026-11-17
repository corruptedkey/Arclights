package main

import (
	"arclights/networks"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClearDownloadCacheIncompleteGame(t *testing.T) {
	tmpDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tmpDir, ".arclights.progress.json"), []byte(`{"status":"downloading"}`), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "config.ini"), []byte("[Game]\nversion=1.6.4\nentry=Endfield.exe\n"), 0644)
	subDir := filepath.Join(tmpDir, "Endfield_Data", "StreamingAssets")
	_ = os.MkdirAll(subDir, 0755)
	_ = os.WriteFile(filepath.Join(subDir, "asset.chk.arclights.tmp"), []byte("partial data"), 0644)
	_ = os.WriteFile(filepath.Join(subDir, "finished_part.chk"), []byte("finished data"), 0644)

	s := &LauncherService{}

	cfg, err := s.DetectInstalledConfig(tmpDir)
	if err != nil {
		t.Fatalf("expected config to be detected: %v", err)
	}
	if s.isGameInstalled(tmpDir, cfg) {
		t.Fatalf("game should NOT be considered installed when Endfield.exe is missing")
	}

	err = s.ClearDownloadCache(tmpDir)
	if err != nil {
		t.Fatalf("ClearDownloadCache failed: %v", err)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read tmpDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected tmpDir to be empty after clearing cache, got %d items", len(entries))
	}
}

func TestClearDownloadCacheCompleteGame(t *testing.T) {
	tmpDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tmpDir, "config.ini"), []byte("[Game]\nversion=1.6.4\nentry=Endfield.exe\n"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "Endfield.exe"), []byte("MZ..."), 0755)
	subDir := filepath.Join(tmpDir, "Endfield_Data")
	_ = os.MkdirAll(subDir, 0755)
	_ = os.WriteFile(filepath.Join(subDir, "main.data"), []byte("game data"), 0644)
	_ = os.WriteFile(filepath.Join(subDir, "temp.arclights.tmp"), []byte("leftover"), 0644)

	s := &LauncherService{}

	cfg, _ := s.DetectInstalledConfig(tmpDir)
	if !s.isGameInstalled(tmpDir, cfg) {
		t.Fatalf("game SHOULD be considered installed when Endfield.exe exists")
	}

	err := s.ClearDownloadCache(tmpDir)
	if err != nil {
		t.Fatalf("ClearDownloadCache failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "Endfield.exe")); err != nil {
		t.Fatalf("Endfield.exe was unexpectedly deleted")
	}
	if _, err := os.Stat(filepath.Join(subDir, "main.data")); err != nil {
		t.Fatalf("main.data was unexpectedly deleted")
	}

	if _, err := os.Stat(filepath.Join(subDir, "temp.arclights.tmp")); !os.IsNotExist(err) {
		t.Fatalf("temp.arclights.tmp should have been deleted")
	}
}

func TestUninstallGame(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpDir, "config.ini"), []byte("[Game]\nversion=1.6.4\nentry=Endfield.exe\n"), 0644); err != nil {
		t.Fatalf("create config fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "Endfield.exe"), []byte("MZ..."), 0755); err != nil {
		t.Fatalf("create executable fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".arclights.progress.json"), []byte(`{"status":"ready"}`), 0644); err != nil {
		t.Fatalf("create progress fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".arclights.state.json"), []byte(`{}`), 0644); err != nil {
		t.Fatalf("create state fixture: %v", err)
	}
	subDir := filepath.Join(tmpDir, "Endfield_Data")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("create game data directory fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "main.data"), []byte("game data"), 0644); err != nil {
		t.Fatalf("create game data fixture: %v", err)
	}

	s := &LauncherService{}

	err := s.UninstallGame(tmpDir)
	if err != nil {
		t.Fatalf("UninstallGame failed: %v", err)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read tmpDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected tmpDir to be completely empty after uninstall, got %d items", len(entries))
	}
}

func TestUninstallGameRejectsUnrecognizedDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	unrelatedFile := filepath.Join(tmpDir, "do-not-delete.txt")
	if err := os.WriteFile(unrelatedFile, []byte("keep"), 0644); err != nil {
		t.Fatalf("create unrelated fixture: %v", err)
	}

	err := (&LauncherService{}).UninstallGame(tmpDir)
	if err == nil {
		t.Fatal("expected uninstall of an unrecognized directory to fail")
	}
	if _, err := os.Stat(unrelatedFile); err != nil {
		t.Fatalf("unrecognized-directory file was removed: %v", err)
	}
}

func TestCancelOperationAndWaitWaitsForOperationCompletion(t *testing.T) {
	s := &LauncherService{}
	op, err := s.beginOperation()
	if err != nil {
		t.Fatalf("begin operation: %v", err)
	}

	finished := make(chan struct{})
	go func() {
		s.cancelOperationAndWait()
		close(finished)
	}()

	select {
	case <-op.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("operation context was not cancelled")
	}
	select {
	case <-finished:
		t.Fatal("cancellation returned before the operation finished")
	default:
	}

	s.finishOperation(op)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not return after the operation finished")
	}
}

func TestPowerShellSingleQuoted(t *testing.T) {
	path := "C:\\Users\\O'Brien\\$Desktop`Redirected"
	if got, want := powerShellSingleQuoted(path), "'C:\\Users\\O''Brien\\$Desktop`Redirected'"; got != want {
		t.Fatalf("PowerShell path quoting = %q, want %q", got, want)
	}
}

func TestDetectEncryptedConfigAndInstalledStatus(t *testing.T) {
	s := &LauncherService{}
	cfg, err := s.DetectInstalledConfig(`E:\Arknights Endfield`)
	if err != nil {
		t.Logf("real game dir not found or error: %v", err)
		return
	}
	t.Logf("Detected config: %+v", cfg)
	if cfg.Version != "1.6.4" {
		t.Fatalf("expected version 1.6.4, got %s", cfg.Version)
	}
	if !s.isGameInstalled(`E:\Arknights Endfield`, cfg) {
		t.Fatalf("expected game to be considered installed")
	}

	client, _ := networks.NewLauncherClient()
	s.client = client
	info, err := s.GetGameState()
	if err != nil {
		t.Fatalf("GetGameState failed: %v", err)
	}
	t.Logf("GetGameState result: %+v", info)
	if info.Status != "ready" && info.Status != "running" {
		t.Fatalf("expected Status ready or running, got %s", info.Status)
	}
}

func TestIsProcessRunning(t *testing.T) {
	if !isProcessRunning("explorer.exe") {
		t.Fatalf("expected explorer.exe to be running on Windows")
	}
	if isProcessRunning("definitely_not_a_real_process_xyz123.exe") {
		t.Fatalf("did not expect non-existent process to be running")
	}
}

func TestLauncherServiceWindowVisibilityMethods(t *testing.T) {
	s := &LauncherService{}

	s.HideWindow()
	s.ShowWindow()

	s.SetMainWindow(nil)
	s.HideWindow()
	s.ShowWindow()
}

func TestBuildLaunchArguments(t *testing.T) {
	tmpDir := t.TempDir()
	s := &LauncherService{}

	s.saveSettings(LauncherSettings{
		RenderingMode: "vk",
		LaunchCommand: "",
	})
	_ = os.Setenv("LOCALAPPDATA", tmpDir)
	defer os.Unsetenv("LOCALAPPDATA")

	args := s.buildLaunchArguments("")
	if strings.Contains(args, "-dx11") {
		t.Fatalf("expected no -dx11 for Vulkan, got %q", args)
	}

	_ = s.SaveLauncherSettings(LauncherSettings{
		RenderingMode: "dx",
		LaunchCommand: "",
	})
	args = s.buildLaunchArguments("")
	if !strings.Contains(args, "-dx11") {
		t.Fatalf("expected -dx11 for DirectX 11, got %q", args)
	}

	_ = s.SaveLauncherSettings(LauncherSettings{
		RenderingMode: "dx",
		LaunchCommand: "-screen-width 1920",
	})
	args = s.buildLaunchArguments("")
	if !strings.Contains(args, "-dx11") || !strings.Contains(args, "-screen-width 1920") {
		t.Fatalf("expected -dx11 and custom args, got %q", args)
	}
}

func TestKillProcessByPID(t *testing.T) {
	killProcessByPID(0)
	killProcessByPID(-1)
	killProcessByPID(99999999)
}
