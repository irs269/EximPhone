// Package crypto provides AES-256-GCM sealing for data at rest (chat messages).
// The key is supplied by the environment, never stored in the database.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

type Box struct{ aead cipher.AEAD }

// NewBox builds a Box from a 64-character hex string (32 bytes).
func NewBox(hexKey string) (*Box, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("key must be 64 hex characters (32 bytes)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal returns nonce||ciphertext. aad binds the ciphertext to its context
// (e.g. the conversation id) so rows cannot be moved between conversations.
func (b *Box) Seal(plain, aad []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plain, aad), nil
}

func (b *Box) Open(sealed, aad []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("ciphertext too short")
	}
	return b.aead.Open(nil, sealed[:n], sealed[n:], aad)
}
