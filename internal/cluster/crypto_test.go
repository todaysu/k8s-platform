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
	first, err := cipher.Encrypt(plaintext, "c1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := cipher.Encrypt(plaintext, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("expected random nonces to produce different ciphertext")
	}
	if strings.Contains(first, string(plaintext)) {
		t.Fatal("ciphertext contains plaintext")
	}
	decrypted, err := cipher.Decrypt(first, "c1")
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

func TestCredentialIsBoundToItsCluster(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	cipher, err := NewAESGCMCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Encrypt([]byte(`{"token":"secret"}`), "cluster-a")
	if err != nil {
		t.Fatal(err)
	}
	opened, err := cipher.Decrypt(sealed, "cluster-a")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, []byte(`{"token":"secret"}`)) {
		t.Fatalf("round trip mismatch: %q", opened)
	}
	// Replaying the row under a different cluster must fail authentication:
	// that is the point of binding ciphertext to the record.
	if _, err = cipher.Decrypt(sealed, "cluster-b"); err == nil {
		t.Fatal("credential opened under a different cluster id")
	}
}

func TestCredentialRefusesEmptyClusterID(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	cipher, err := NewAESGCMCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	// Sealing without a binding would create a row any cluster could claim.
	if _, err := cipher.Encrypt([]byte("x"), ""); err == nil {
		t.Fatal("sealing without a cluster id must be rejected")
	}
}

func TestCredentialRejectsGarbage(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	cipher, err := NewAESGCMCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "not-base64!!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := cipher.Decrypt(value, "cluster-a"); err == nil {
			t.Fatalf("expected failure for %q", value)
		}
	}
}
