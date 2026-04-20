package crypto

import (
	"bytes"
	"testing"
)

const devKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestRoundTrip(t *testing.T) {
	c, err := NewFromHex(devKey)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte(`{"api_token":"abc-123"}`)
	ct, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(ct, plaintext) {
		t.Fatal("ciphertext equals plaintext")
	}

	pt, err := c.Decrypt(ct)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Fatalf("round-trip mismatch: got %q want %q", pt, plaintext)
	}
}

func TestNonceUnique(t *testing.T) {
	c, _ := NewFromHex(devKey)
	a, _ := c.Encrypt([]byte("hello"))
	b, _ := c.Encrypt([]byte("hello"))
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of same plaintext produced identical output — nonce reuse")
	}
}

func TestBadKey(t *testing.T) {
	if _, err := NewFromHex("short"); err == nil {
		t.Fatal("expected error for short key")
	}
	if _, err := NewFromHex(string(bytes.Repeat([]byte("z"), 64))); err == nil {
		t.Fatal("expected error for non-hex key")
	}
}

func TestDecryptShortCiphertext(t *testing.T) {
	c, _ := NewFromHex(devKey)
	if _, err := c.Decrypt([]byte{1, 2}); err == nil {
		t.Fatal("expected error for truncated ciphertext")
	}
}
