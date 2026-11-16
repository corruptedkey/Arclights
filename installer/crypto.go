package installer

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"fmt"
)

// Default AES-256-CBC Key and IV extracted from Gryphlink client (Games.exe).
var (
	DefaultAESKey, _ = hex.DecodeString("c0f30e1ce763bbc21cc355a34303ac50399444bff68c4a22af398c0a166ee143")
	DefaultAESIV, _  = hex.DecodeString("33467861192750649501937264608400")
)

func DecryptAES256CBC(ciphertext, key, iv []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, errors.New("empty ciphertext")
	}
	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext length %d is not a multiple of AES block size %d", len(ciphertext), aes.BlockSize)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	decrypted := make([]byte, len(ciphertext))
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(decrypted, ciphertext)

	// PKCS#7 unpadding
	padLen := int(decrypted[len(decrypted)-1])
	if padLen == 0 || padLen > aes.BlockSize {
		return decrypted, nil
	}
	for i := len(decrypted) - padLen; i < len(decrypted); i++ {
		if decrypted[i] != byte(padLen) {
			// Not standard PKCS#7 padding, return raw decrypted
			return decrypted, nil
		}
	}
	return bytes.TrimRight(decrypted[:len(decrypted)-padLen], "\x00"), nil
}
