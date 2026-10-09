package ilink

import (
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// EncryptAES128ECB encrypts data using AES-128 in ECB mode with PKCS7 padding.
func EncryptAES128ECB(data, key []byte) ([]byte, error) {
	if len(key) != 16 {
		return nil, fmt.Errorf("AES-128 key must be 16 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(data, aes.BlockSize)
	out := make([]byte, len(padded))
	for i := 0; i < len(padded); i += aes.BlockSize {
		block.Encrypt(out[i:], padded[i:])
	}
	return out, nil
}

// DecryptAES128ECB decrypts AES-128-ECB ciphertext with PKCS7 unpadding.
func DecryptAES128ECB(data, key []byte) ([]byte, error) {
	if len(key) != 16 {
		return nil, fmt.Errorf("AES-128 key must be 16 bytes, got %d", len(key))
	}
	if len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext length %d not a multiple of block size", len(data))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += aes.BlockSize {
		block.Decrypt(out[i:], data[i:])
	}
	return pkcs7Unpad(out)
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	padded := make([]byte, len(data)+pad)
	copy(padded, data)
	for i := len(data); i < len(padded); i++ {
		padded[i] = byte(pad)
	}
	return padded
}

func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(data) {
		return nil, fmt.Errorf("invalid padding %d", pad)
	}
	for i := len(data) - pad; i < len(data); i++ {
		if data[i] != byte(pad) {
			return nil, fmt.Errorf("invalid padding byte at position %d", pad)
		}
	}
	return data[:len(data)-pad], nil
}

// ParseAESKey decodes an AES-128 key from the string formats used by WeChat CDN:
//  1. raw 16-byte string
//  2. 32 hex characters
//  3. base64(16 raw bytes)
//  4. base64(32 hex characters)
func ParseAESKey(s string) ([]byte, error) {
	b := []byte(s)
	if len(b) == 16 {
		out := make([]byte, 16)
		copy(out, b)
		return out, nil
	}
	if len(b) == 32 && isHexBytes(b) {
		dst := make([]byte, 16)
		if _, err := hex.Decode(dst, b); err != nil {
			return nil, fmt.Errorf("hex decode aes_key: %w", err)
		}
		return dst, nil
	}

	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		// Try raw (no-padding) standard base64 as a fallback before giving up.
		decoded, err = base64.RawStdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("base64 decode aes_key: %w", err)
		}
	}
	// If the decoded bytes look like a hex string (32 ASCII hex chars → 16-byte key),
	// hex-decode them.
	if len(decoded) == 32 && isHexBytes(decoded) {
		dst := make([]byte, 16)
		if _, err := hex.Decode(dst, decoded); err != nil {
			return nil, fmt.Errorf("hex decode aes_key: %w", err)
		}
		return dst, nil
	}
	return decoded, nil
}

// isHexBytes reports whether every byte in b is a valid lowercase or uppercase hex digit.
func isHexBytes(b []byte) bool {
	for _, c := range b {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
