package networks

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFullInstallSizesAsStrings(t *testing.T) {
	raw := `{
		"action": 1,
		"version": "1.6.4",
		"patch": null,
		"pkg": {
			"total_size": "130924997432",
			"packs": [
				{ "url": "https://cdn/x.zip.001", "md5": "aa", "package_size": "1073741824" },
				{ "url": "https://cdn/x.zip.002", "md5": "bb", "package_size": 1073741824 }
			]
		}
	}`

	var rsp GameRsp
	if err := json.Unmarshal([]byte(raw), &rsp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if rsp.Action == nil || rsp.Action.Uint64() != 1 {
		t.Fatalf("expected action 1, got %v", rsp.Action)
	}

	up := rsp.Update()
	if !up.IsFull() {
		t.Fatalf("expected UpdateTypeFull, got %v", up.Type)
	}

	if len(up.Pkg.Packs) != 2 {
		t.Fatalf("expected 2 packs, got %d", len(up.Pkg.Packs))
	}

	if up.Pkg.DownloadSize() != 2*1073741824 {
		t.Fatalf("expected download size %d, got %d", 2*1073741824, up.Pkg.DownloadSize())
	}

	if up.Pkg.TotalSize == nil || up.Pkg.TotalSize.Uint64() != 130924997432 {
		t.Fatalf("expected total size 130924997432, got %v", up.Pkg.TotalSize)
	}
}

func TestPatchPriorityOverPkg(t *testing.T) {
	raw := `{
		"action": "1",
		"version": "1.6.4",
		"patch": {
			"url": "https://cdn/p.zip",
			"md5": "cc",
			"package_size": "3145728",
			"total_size": "7760426",
			"patches": []
		},
		"pkg": {
			"packs": [{ "url": "u", "package_size": "1" }]
		}
	}`

	var rsp GameRsp
	if err := json.Unmarshal([]byte(raw), &rsp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if rsp.Action == nil || rsp.Action.Uint64() != 1 {
		t.Fatalf("expected action 1, got %v", rsp.Action)
	}

	up := rsp.Update()
	if !up.IsPatch() {
		t.Fatalf("expected UpdateTypePatch, got %v", up.Type)
	}

	if up.Patch.PackageSize.Uint64() != 3145728 {
		t.Fatalf("expected package_size 3145728, got %d", up.Patch.PackageSize.Uint64())
	}

	if up.Patch.TotalSize == nil || up.Patch.TotalSize.Uint64() != 7760426 {
		t.Fatalf("expected total_size 7760426, got %v", up.Patch.TotalSize)
	}
}

func TestUpToDateAndEmptyObjects(t *testing.T) {
	raw := `{"action": 0, "version": "1.6.4", "patch": null}`
	var rsp GameRsp
	if err := json.Unmarshal([]byte(raw), &rsp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !rsp.Update().IsUpToDate() {
		t.Fatalf("expected IsUpToDate() == true")
	}

	// Empty objects should not crash and should also indicate up-to-date
	rawEmpty := `{"patch": {}, "pkg": {}}`
	var rspEmpty GameRsp
	if err := json.Unmarshal([]byte(rawEmpty), &rspEmpty); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !rspEmpty.Update().IsUpToDate() {
		t.Fatalf("expected empty objects to mean up to date")
	}
}

func TestPrePatchAndUnknownFields(t *testing.T) {
	raw := `{
		"version": "1.7.0",
		"pre_patch": {"download": true},
		"custom_field": "hello_world"
	}`

	var rsp GameRsp
	if err := json.Unmarshal([]byte(raw), &rsp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if !rsp.HasPrePatch() {
		t.Fatalf("expected HasPrePatch() == true")
	}

	if val, ok := rsp.Extra["custom_field"]; !ok || val != "hello_world" {
		t.Fatalf("expected custom_field to be kept in Extra, got %v", val)
	}
}

func TestRequestShape(t *testing.T) {
	req := NewEndfieldGameRequest("1.6.3")
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if m["appcode"] != EndfieldAppCode {
		t.Fatalf("expected appcode %s, got %v", EndfieldAppCode, m["appcode"])
	}
	if m["version"] != "1.6.3" {
		t.Fatalf("expected version 1.6.3, got %v", m["version"])
	}
	if m["disk_type"] != float64(0) {
		t.Fatalf("expected disk_type 0, got %v", m["disk_type"])
	}
	if m["patch_encrypt"] != true {
		t.Fatalf("expected patch_encrypt true, got %v", m["patch_encrypt"])
	}
}

func TestWebCommonReqDefaults(t *testing.T) {
	req := NewEndfieldWebCommonReq()
	if req.AppCode != EndfieldAppCode {
		t.Fatalf("expected appcode %s, got %s", EndfieldAppCode, req.AppCode)
	}
	if req.Language != "en-us" {
		t.Fatalf("expected language en-us, got %s", req.Language)
	}
	if req.Channel != "6" || req.SubChannel != "6" {
		t.Fatalf("expected channel 6/6, got %s/%s", req.Channel, req.SubChannel)
	}
	if req.Platform != "Windows" || req.Source != "launcher" {
		t.Fatalf("expected Windows/launcher, got %s/%s", req.Platform, req.Source)
	}
}

func TestConfigIniParsing(t *testing.T) {
	tmpDir := t.TempDir()
	iniContent := `
[General]
channel=1

[Game]
version=1.6.4
entry=Endfield.exe
entry_md5=d41d8cd98f00b204e9800998ecf8427e
appcode=YDUTE5gscDZ229CW
channel=6
sub_channel=6
region=prod
uninstall_params=--uninstall
`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.ini"), []byte(iniContent), 0644); err != nil {
		t.Fatalf("write file error: %v", err)
	}

	cfg, err := ReadInstalledGameConfig(tmpDir)
	if err != nil {
		t.Fatalf("ReadInstalledGameConfig error: %v", err)
	}

	if cfg.Version != "1.6.4" {
		t.Fatalf("expected version 1.6.4, got %s", cfg.Version)
	}
	if cfg.Entry != "Endfield.exe" {
		t.Fatalf("expected entry Endfield.exe, got %s", cfg.Entry)
	}
	if cfg.AppCode != EndfieldAppCode {
		t.Fatalf("expected appcode %s, got %s", EndfieldAppCode, cfg.AppCode)
	}
	if cfg.Channel != "6" || cfg.SubChannel != "6" {
		t.Fatalf("expected channel 6/6, got %s/%s", cfg.Channel, cfg.SubChannel)
	}

	block, _ := aes.NewCipher(defaultAESKey)
	pad := aes.BlockSize - len(iniContent)%aes.BlockSize
	padded := append([]byte(iniContent), bytes.Repeat([]byte{byte(pad)}, pad)...)
	enc := make([]byte, len(padded))
	mode := cipher.NewCBCEncrypter(block, defaultAESIV)
	mode.CryptBlocks(enc, padded)

	tmpDirEnc := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDirEnc, "config.ini"), enc, 0644); err != nil {
		t.Fatalf("write encrypted file error: %v", err)
	}

	cfgEnc, err := ReadInstalledGameConfig(tmpDirEnc)
	if err != nil {
		t.Fatalf("ReadInstalledGameConfig encrypted error: %v", err)
	}
	if cfgEnc.Version != "1.6.4" || cfgEnc.Entry != "Endfield.exe" {
		t.Fatalf("failed to parse encrypted config.ini: %+v", cfgEnc)
	}
}

func TestLiveGetLatestGame(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live network test in short mode")
	}

	client, err := NewLauncherClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rspFull, err := client.GetLatestGame(ctx, "")
	if err != nil {
		t.Fatalf("GetLatestGame('') failed: %v", err)
	}
	if rspFull.Version == "" {
		t.Fatalf("expected non-empty version")
	}
	upFull := rspFull.Update()
	if !upFull.IsFull() {
		t.Fatalf("expected full install update, got %v", upFull.Type)
	}
	if len(upFull.Pkg.Packs) == 0 {
		t.Fatalf("expected packs to be populated")
	}

	rspPatch, err := client.GetLatestGame(ctx, "1.6.3")
	if err != nil {
		t.Fatalf("GetLatestGame('1.6.3') failed: %v", err)
	}
	upPatch := rspPatch.Update()
	if !upPatch.IsPatch() {
		t.Fatalf("expected patch update from 1.6.3, got %v", upPatch.Type)
	}
	if upPatch.Patch.URL == "" {
		t.Fatalf("expected patch URL to be populated")
	}

	rspCurrent, err := client.GetLatestGame(ctx, rspFull.Version)
	if err != nil {
		t.Fatalf("GetLatestGame('%s') failed: %v", rspFull.Version, err)
	}
	if !rspCurrent.Update().IsUpToDate() {
		t.Fatalf("expected IsUpToDate() for current version %s", rspFull.Version)
	}
}
