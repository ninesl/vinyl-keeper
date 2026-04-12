package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

const (
	// encryptionKeySize is 32 bytes for AES-256
	encryptionKeySize = 32
)

var (
	// encryptionKey is generated once at startup and stored in memory
	encryptionKey []byte
)

func init() {
	// Generate a cryptographically secure random key on startup
	var err error
	encryptionKey, err = generateEncryptionKey()
	if err != nil {
		panic(fmt.Sprintf("failed to generate encryption key: %v", err))
	}
}

// generateEncryptionKey creates a cryptographically secure random key for AES encryption
func generateEncryptionKey() ([]byte, error) {
	key := make([]byte, encryptionKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate encryption key: %w", err)
	}
	return key, nil
}

// Encrypt encrypts data using AES-256-GCM and returns base64-encoded ciphertext
// The format is: nonce (12 bytes) || ciphertext || tag (16 bytes)
func Encrypt(plaintext []byte) (string, error) {
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Seal appends the authentication tag to the ciphertext
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)

	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts base64-encoded ciphertext that was encrypted with Encrypt
func Decrypt(ciphertextBase64 string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}

	return plaintext, nil
}
