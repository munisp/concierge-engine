// Field-level encryption for VIP-sensitive data (Master Spec section 13).
// Envelope: AES-256-GCM, random nonce per value, key from CE_FIELD_KEY
// (base64, 32 bytes). Ciphertexts carry an "enc:v1:" prefix so plaintext and
// ciphertext rows coexist during rotation; decryption is prefix-detected.
// With no key configured the cipher is disabled and values pass through
// unchanged — dev default, never acceptable in production (see docs).
package domain

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

const encPrefix = "enc:v1:"

type FieldCipher struct {
	key []byte // nil = disabled
}

func NewFieldCipher(keyB64 string) (*FieldCipher, error) {
	if keyB64 == "" {
		return &FieldCipher{}, nil
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, fmt.Errorf("CE_FIELD_KEY: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("CE_FIELD_KEY: need 32 bytes, got %d", len(key))
	}
	return &FieldCipher{key: key}, nil
}

func (c *FieldCipher) Enabled() bool { return c != nil && len(c.key) == 32 }

// Encrypt returns "enc:v1:<base64(nonce|ciphertext)>".
func (c *FieldCipher) Encrypt(plaintext string) (string, error) {
	if !c.Enabled() {
		return plaintext, nil
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(ct), nil
}

// Decrypt reverses Encrypt; values without the prefix pass through unchanged
// (mixed plaintext/ciphertext rows during key rotation).
func (c *FieldCipher) Decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, encPrefix) {
		return value, nil
	}
	if !c.Enabled() {
		return "", fmt.Errorf("encrypted value but CE_FIELD_KEY not configured")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, encPrefix))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}
	pt, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(pt), nil
}
