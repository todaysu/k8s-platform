package cluster

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

type CredentialCipher interface {
	Encrypt([]byte) (string, error)
	Decrypt(string) ([]byte, error)
}

type AESGCMCipher struct {
	aead cipher.AEAD
}

func NewAESGCMCipher(encodedKey string) (*AESGCMCipher, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("credential encryption key must be base64-encoded 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create credential gcm: %w", err)
	}
	return &AESGCMCipher{aead: aead}, nil
}

func (cipher *AESGCMCipher) Encrypt(plaintext []byte) (string, error) {
	nonce := make([]byte, cipher.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", newError(CodeEncryptionFailed)
	}
	sealed := cipher.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (cipher *AESGCMCipher) Decrypt(encoded string) ([]byte, error) {
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < cipher.aead.NonceSize() {
		return nil, newError(CodeEncryptionFailed)
	}
	nonce := sealed[:cipher.aead.NonceSize()]
	ciphertext := sealed[cipher.aead.NonceSize():]
	plaintext, err := cipher.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, newError(CodeEncryptionFailed)
	}
	return plaintext, nil
}
