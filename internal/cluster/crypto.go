package cluster

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// CredentialCipher seals and opens cluster credentials. The cluster id is part
// of every call so the ciphertext is bound to exactly one record.
type CredentialCipher interface {
	Encrypt(plaintext []byte, clusterID string) (string, error)
	Decrypt(encoded, clusterID string) ([]byte, error)
	// NeedsRebind reports whether a stored value predates AAD binding.
	NeedsRebind(encoded string) bool
}

// boundCredentialCipher binds ciphertext to the record it belongs to by using
// the cluster id as AEAD additional authenticated data.
//
// Without AAD, a ciphertext row can be copied to another cluster's record and
// still decrypt cleanly, so a database write turns into silent credential
// substitution. With AAD, the same row moved elsewhere fails to authenticate.
//
// Legacy ciphertext produced before AAD was introduced carries no marker and
// is still accepted on read, so upgrading does not invalidate enrolled
// clusters; it is re-encrypted with AAD the next time it is written.
type boundCredentialCipher struct {
	aead cipher.AEAD
	// legacy seals and opens without additional data.
	legacy cipher.AEAD
}

// aadPrefix marks a record as AAD-sealed. Without an explicit marker the
// reader cannot tell an AAD-sealed blob from a legacy one.
const aadPrefix = "v2:"

type AESGCMCipher struct {
	*boundCredentialCipher
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
	return &AESGCMCipher{boundCredentialCipher: &boundCredentialCipher{aead: aead, legacy: aead}}, nil
}

// Encrypt seals plaintext for the given cluster. The cluster id is mandatory:
// sealing without it would produce a credential row that any cluster could
// claim, which is the exact confusion AAD is meant to prevent.
func (cipher *boundCredentialCipher) Encrypt(plaintext []byte, clusterID string) (string, error) {
	if clusterID == "" {
		return "", newError(CodeEncryptionFailed)
	}
	nonce := make([]byte, cipher.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", newError(CodeEncryptionFailed)
	}
	sealed := cipher.aead.Seal(nonce, nonce, plaintext, []byte(clusterID))
	return aadPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func (cipher *boundCredentialCipher) Decrypt(encoded, clusterID string) ([]byte, error) {
	if encoded == "" {
		return nil, newError(CodeEncryptionFailed)
	}
	bound := len(encoded) > len(aadPrefix) && encoded[:len(aadPrefix)] == aadPrefix
	payload := encoded
	target := cipher.aead
	if bound {
		payload = encoded[len(aadPrefix):]
	} else {
		// Pre-AAD ciphertext: no cluster binding is available on the wire.
		target = cipher.legacy
	}
	sealed, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(sealed) < target.NonceSize() {
		return nil, newError(CodeEncryptionFailed)
	}
	nonce := sealed[:target.NonceSize()]
	ciphertext := sealed[target.NonceSize():]
	var additional []byte
	if bound {
		additional = []byte(clusterID)
	}
	plaintext, err := target.Open(nil, nonce, ciphertext, additional)
	if err != nil {
		return nil, newError(CodeEncryptionFailed)
	}
	return plaintext, nil
}

// NeedsRebind reports whether a stored credential is still a legacy blob and
// should be rewritten with AAD on the next successful use.
func (cipher *boundCredentialCipher) NeedsRebind(encoded string) bool {
	return !(len(encoded) > len(aadPrefix) && encoded[:len(aadPrefix)] == aadPrefix)
}
