package auth

import (
	"fmt"
	"net/http"
	"os"
)

const (
	// CookieName is the name of the session cookie
	CookieName = "vk_session"
	// CookiePath is the path for the session cookie
	CookiePath = "/"
)

// SessionUser holds the authenticated user information extracted from the session
type SessionUser struct {
	UserID   int64
	Username string
}

// CreateSessionCookie creates an encrypted JWT session cookie for the user
func CreateSessionCookie(w http.ResponseWriter, userID int64, username string) error {
	// Create JWT
	token, err := SignJWT(userID, username)
	if err != nil {
		return fmt.Errorf("failed to sign JWT: %w", err)
	}

	// Encrypt the JWT
	encryptedToken, err := Encrypt([]byte(token))
	if err != nil {
		return fmt.Errorf("failed to encrypt token: %w", err)
	}

	// Determine if we should use Secure flag based on TLS setting
	secure := os.Getenv("ENABLE_TLS") == "true"

	// Set cookie
	cookie := &http.Cookie{
		Name:     CookieName,
		Value:    encryptedToken,
		Path:     CookiePath,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		// No MaxAge or Expires - session cookie (deleted when browser closes)
	}

	http.SetCookie(w, cookie)
	return nil
}

// ClearSessionCookie deletes the session cookie (sign out)
func ClearSessionCookie(w http.ResponseWriter) {
	cookie := &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     CookiePath,
		MaxAge:   -1, // Delete immediately
		HttpOnly: true,
		Secure:   os.Getenv("ENABLE_TLS") == "true",
		SameSite: http.SameSiteStrictMode,
	}

	http.SetCookie(w, cookie)
}

// GetSessionUser extracts and validates the user from the session cookie
// Returns nil SessionUser and no error if no valid session exists (anonymous user)
func GetSessionUser(r *http.Request) (*SessionUser, error) {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		// No cookie = anonymous user
		if err == http.ErrNoCookie {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read cookie: %w", err)
	}

	// Decrypt the token
	decryptedToken, err := Decrypt(cookie.Value)
	if err != nil {
		return nil, nil // Invalid/malformed cookie = anonymous user
	}

	// Validate the JWT
	claims, err := ValidateJWT(string(decryptedToken))
	if err != nil {
		return nil, nil // Invalid JWT = anonymous user
	}

	return &SessionUser{
		UserID:   claims.UserID,
		Username: claims.Username,
	}, nil
}
