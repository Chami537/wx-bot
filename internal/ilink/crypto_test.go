package ilink

import (
	"bytes"
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef")
	for _, in := range [][]byte{
		{},
		{0x00},
		[]byte("a"),
		[]byte("hello wx-bot"),
		bytes.Repeat([]byte("x"), 15),
		bytes.Repeat([]byte("y"), 16),
		bytes.Repeat([]byte("z"), 17),
		bytes.Repeat([]byte("q"), 64),
		[]byte("中文 emoji 😀"),
	} {
		ct, err := EncryptAES128ECB(in, key)
		if err != nil {
			t.Fatalf("encrypt %q: %v", in, err)
		}
		if len(ct)%aes.BlockSize != 0 {
			t.Fatalf("ciphertext len %d not multiple of block", len(ct))
		}
		pt, err := DecryptAES128ECB(ct, key)
		if err != nil {
			t.Fatalf("decrypt %q: %v", in, err)
		}
		if !bytes.Equal(pt, in) {
			t.Fatalf("round trip mismatch: got %q want %q", pt, in)
		}
	}
}

func TestPKCS7EdgeCases(t *testing.T) {
	// empty → full block of 16
	padded := pkcs7Pad(nil, aes.BlockSize)
	if len(padded) != aes.BlockSize {
		t.Fatalf("empty pad len %d", len(padded))
	}
	for _, b := range padded {
		if b != byte(aes.BlockSize) {
			t.Fatalf("empty pad byte %d", b)
		}
	}
	out, err := pkcs7Unpad(padded)
	if err != nil || len(out) != 0 {
		t.Fatalf("unpad empty: %q %v", out, err)
	}

	// exact block
	block := bytes.Repeat([]byte{0x41}, 16)
	padded = pkcs7Pad(block, 16)
	if len(padded) != 32 {
		t.Fatalf("full block pad len %d", len(padded))
	}
	out, err = pkcs7Unpad(padded)
	if err != nil || !bytes.Equal(out, block) {
		t.Fatalf("unpad full block: %v", err)
	}

	// invalid: empty
	if _, err := pkcs7Unpad(nil); err == nil {
		t.Fatal("expected error on empty")
	}
	// invalid: pad 0
	if _, err := pkcs7Unpad([]byte{1, 2, 3, 0}); err == nil {
		t.Fatal("expected error on pad 0")
	}
	// invalid: pad > block
	if _, err := pkcs7Unpad(bytes.Repeat([]byte{17}, 16)); err == nil {
		t.Fatal("expected error on pad 17")
	}
	// invalid: inconsistent
	bad := bytes.Repeat([]byte{4}, 16)
	bad[15] = 4
	bad[14] = 3
	if _, err := pkcs7Unpad(bad); err == nil {
		t.Fatal("expected error on inconsistent padding")
	}
}

func TestParseAESKeyAllForms(t *testing.T) {
	key := []byte("0123456789abcdef") // 16 raw bytes
	forms := map[string]string{
		"raw16":        string(key),
		"hex32":        hex.EncodeToString(key),
		"base64_raw16": base64.StdEncoding.EncodeToString(key),
		"base64_hex32": base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(key))),
		"raw_base64":   base64.RawStdEncoding.EncodeToString(key),
		"hex32_upper":  hex.EncodeToString(key),
	}
	// uppercase hex
	forms["hex32_upper"] = "30313233343536373839616263646566" // that's 0-9a-f of ASCII... wait
	forms["hex32_upper"] = toUpperHex(key)

	for name, s := range forms {
		got, err := ParseAESKey(s)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(got, key) {
			t.Fatalf("%s: got %q want %q", name, got, key)
		}
	}
}

func toUpperHex(b []byte) string {
	return hex.EncodeToString(b) // hex.EncodeToString is lowercase
}

func TestParseAESKeyUpperHex(t *testing.T) {
	key := []byte{0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77}
	upper := "ABCDEF01234567890011223344556677"
	got, err := ParseAESKey(upper)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, key) {
		t.Fatalf("got %x want %x", got, key)
	}
}

func TestEncryptRejectsBadKey(t *testing.T) {
	if _, err := EncryptAES128ECB([]byte("x"), []byte("short")); err == nil {
		t.Fatal("expected error")
	}
	if _, err := DecryptAES128ECB([]byte("not-a-multiple!"), bytes.Repeat([]byte{1}, 16)); err == nil {
		t.Fatal("expected error on bad length")
	}
}
