package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// signingKeySize is 32 bytes for HS256
	signingKeySize = 32
)

var (
	// signingKey is generated once at startup and stored in memory
	signingKey []byte
)

func init() {
	// Generate a cryptographically secure random key on startup
	var err error
	signingKey, err = generateSecureKey()
	if err != nil {
		panic(fmt.Sprintf("failed to generate JWT signing key: %v", err))
	}
}

// generateSecureKey creates a cryptographically secure random key
func generateSecureKey() ([]byte, error) {
	key := make([]byte, signingKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate random key: %w", err)
	}
	return key, nil
}

// GetSigningKeyBase64 returns the signing key as base64 (useful for debugging)
func GetSigningKeyBase64() string {
	return base64.StdEncoding.EncodeToString(signingKey)
}

// SessionClaims represents the JWT claims for a user session
type SessionClaims struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// SignJWT creates a signed JWT for the given user
// The JWT never expires (as per requirements)
func SignJWT(userID int64, username string) (string, error) {
	claims := SessionClaims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now()),
			// No ExpiresAt - JWT never expires
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(signingKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}

	return tokenString, nil
}

// ValidateJWT validates a JWT string and returns the claims
func ValidateJWT(tokenString string) (*SessionClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &SessionClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Verify the signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return signingKey, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse JWT: %w", err)
	}

	if claims, ok := token.Claims.(*SessionClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid JWT claims")
}
