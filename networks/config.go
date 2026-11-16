package networks

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	defaultAESKey, _ = hex.DecodeString("c0f30e1ce763bbc21cc355a34303ac50399444bff68c4a22af398c0a166ee143")
	defaultAESIV, _  = hex.DecodeString("33467861192750649501937264608400")
)

func decryptConfigData(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return data, nil
	}
	block, err := aes.NewCipher(defaultAESKey)
	if err != nil {
		return nil, err
	}
	mode := cipher.NewCBCDecrypter(block, defaultAESIV)
	decrypted := make([]byte, len(data))
	mode.CryptBlocks(decrypted, data)

	// Remove trailing null bytes and PKCS#7 padding if present
	padLen := int(decrypted[len(decrypted)-1])
	if padLen > 0 && padLen <= aes.BlockSize {
		isPad := true
		for i := len(decrypted) - padLen; i < len(decrypted); i++ {
			if decrypted[i] != byte(padLen) {
				isPad = false
				break
			}
		}
		if isPad {
			decrypted = decrypted[:len(decrypted)-padLen]
		}
	}
	return bytes.TrimRight(decrypted, "\x00"), nil
}

// LocalGameConfig holds local game parameters extracted from <GameInstallPath>/config.ini.
type LocalGameConfig struct {
	Version         string
	Entry           string
	EntryMD5        string
	AppCode         string
	Channel         string
	SubChannel      string
	Region          string
	UninstallParams string
}

// ReadInstalledGameConfig reads and parses <installDir>/config.ini under the [Game] section.
// It supports both plaintext INI files and AES-256-CBC encrypted config.ini from the CDN.
func ReadInstalledGameConfig(installDir string) (*LocalGameConfig, error) {
	iniPath := filepath.Join(installDir, "config.ini")
	data, err := os.ReadFile(iniPath)
	if err != nil {
		return nil, fmt.Errorf("config.ini not found: %w", err)
	}

	// If not plaintext INI, attempt AES-256 decryption
	if !bytes.Contains(data, []byte("[Game]")) && !bytes.Contains(data, []byte("[game]")) {
		dec, err := decryptConfigData(data)
		if err == nil && (bytes.Contains(dec, []byte("[Game]")) || bytes.Contains(dec, []byte("[game]"))) {
			data = dec
		}
	}

	cfg := &LocalGameConfig{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	inGameSection := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.TrimSpace(line[1 : len(line)-1])
			inGameSection = strings.EqualFold(section, "Game")
			continue
		}
		if !inGameSection {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch strings.ToLower(key) {
		case "version":
			cfg.Version = val
		case "entry":
			cfg.Entry = val
		case "entry_md5":
			cfg.EntryMD5 = val
		case "appcode":
			cfg.AppCode = val
		case "channel":
			cfg.Channel = val
		case "sub_channel":
			cfg.SubChannel = val
		case "region":
			cfg.Region = val
		case "uninstall_params":
			cfg.UninstallParams = val
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading config.ini: %w", err)
	}

	return cfg, nil
}
