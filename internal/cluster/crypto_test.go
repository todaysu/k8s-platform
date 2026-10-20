package cluster

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestAESGCMCipherRoundTrip(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	cipher, err := NewAESGCMCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("secret-token-and-private-key")
	first, err := cipher.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cipher.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("expected random nonces to produce different ciphertext")
	}
	if strings.Contains(first, string(plaintext)) {
		t.Fatal("ciphertext contains plaintext")
	}
	decrypted, err := cipher.Decrypt(first)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("unexpected plaintext %q", decrypted)
	}
}

func TestAESGCMCipherRejectsInvalidKey(t *testing.T) {
	if _, err := NewAESGCMCipher("not-a-key"); err == nil {
		t.Fatal("expected invalid key error")
	}
}
