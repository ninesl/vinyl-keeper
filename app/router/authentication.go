package router

import (
	"net/http"
	"strconv"

	"github.com/ninesl/vinyl-keeper/app/auth"
	"github.com/ninesl/vinyl-keeper/app/router/assets/ui"
	"github.com/ninesl/vinyl-keeper/app/router/assets/ui/parts"
	"github.com/ninesl/vinyl-keeper/app/router/values"
	"github.com/ninesl/vinyl-keeper/app/vinyl"
)

// DetermineUser extracts the authenticated user from the request
// Returns nil if no valid session exists
func DetermineUser(r *http.Request) *vinyl.User {
	sessionUser, err := auth.GetSessionUser(r)
	if err != nil || sessionUser == nil {
		return nil
	}

	return &vinyl.User{
		UserID:   sessionUser.UserID,
		UserName: sessionUser.Username,
	}
}

// GetUserID extracts the user ID from the session, returns -1 if not authenticated
func GetUserID(r *http.Request) int64 {
	user := DetermineUser(r)
	if user == nil {
		return -1
	}
	return user.UserID
}

// GetUserName extracts the username from the session, returns empty string if not authenticated
func GetUserName(r *http.Request) string {
	user := DetermineUser(r)
	if user == nil {
		return ""
	}
	return user.UserName
}

// IsUserSignedIn checks if a user is currently signed in
func IsUserSignedIn(r *http.Request) bool {
	return GetUserID(r) >= 0
}

// SignInUsersHandler returns the list of users for the sign-in modal
func SignInUsersHandler(k UserLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", values.ContentTypeHTML)

		users, err := k.ListUsers()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			parts.ErrorMessage("Failed to load users").Render(r.Context(), w)
			return
		}

		signedInID := GetUserID(r)
		ui.SignInUsersList(users, signedInID).Render(r.Context(), w)
	}
}

// SignInButtonHandler returns the sign-in button UI (shows username if signed in)
func SignInButtonHandler(k UserGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", values.ContentTypeHTML)

		signedInID := GetUserID(r)
		if signedInID < 0 {
			ui.SignInButtonZone("").Render(r.Context(), w)
			return
		}

		user, err := k.GetUserByID(signedInID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			parts.ErrorMessage("Failed to load signed-in user").Render(r.Context(), w)
			return
		}

		if user == nil {
			ui.SignInButtonZone("").Render(r.Context(), w)
			return
		}

		ui.SignInButtonZone(user.UserName).Render(r.Context(), w)
	}
}

// SignInCreateUserHandler creates a new user
func SignInCreateUserHandler(k UserCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", values.ContentTypeHTML)

		name := r.FormValue(values.QueryUserName)
		if name == "" {
			w.WriteHeader(http.StatusBadRequest)
			parts.ErrorMessage("Missing user name").Render(r.Context(), w)
			return
		}

		created, err := k.CreateUser(name)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			parts.ErrorMessage("Could not create user (name may already exist)").Render(r.Context(), w)
			return
		}

		signedInID := GetUserID(r)
		ui.SignInUserRow(created, signedInID).Render(r.Context(), w)
	}
}

// SignInSubmitHandler handles the actual sign-in (creates encrypted JWT session)
func SignInSubmitHandler(k UserGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", values.ContentTypeHTML)

		userIDStr := r.FormValue(values.QueryUserID)
		if userIDStr == "" {
			w.WriteHeader(http.StatusBadRequest)
			parts.ErrorMessage("Missing user ID").Render(r.Context(), w)
			return
		}

		userID, err := strconv.ParseInt(userIDStr, 10, 64)
		if err != nil || userID <= 0 {
			w.WriteHeader(http.StatusBadRequest)
			parts.ErrorMessage("Invalid user ID").Render(r.Context(), w)
			return
		}

		user, err := k.GetUserByID(userID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			parts.ErrorMessage("Failed to sign in").Render(r.Context(), w)
			return
		}
		if user == nil {
			w.WriteHeader(http.StatusNotFound)
			parts.ErrorMessage("User not found").Render(r.Context(), w)
			return
		}

		// Create encrypted JWT session cookie
		if err := auth.CreateSessionCookie(w, user.UserID, user.UserName); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			parts.ErrorMessage("Failed to create session").Render(r.Context(), w)
			return
		}

		// Trigger HTMX event to update UI
		w.Header().Set(values.HeaderHXTrigger, values.EventUserSignedIn)
		w.WriteHeader(http.StatusOK)
	}
}

// SignOutHandler handles sign-out (clears the session cookie)
func SignOutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", values.ContentTypeHTML)

		// Clear the session cookie
		auth.ClearSessionCookie(w)

		// Trigger HTMX event to update UI
		w.Header().Set(values.HeaderHXTrigger, values.EventUserSignedOut)
		w.WriteHeader(http.StatusOK)

		// Return updated sign-in panel (anonymous)
		ui.SignInPanel("").Render(r.Context(), w)
	}
}

// SignInPanelHandler renders the sign-in panel with current user info
func SignInPanelHandler(getUserName func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", values.ContentTypeHTML)
		userName := getUserName(r)
		ui.SignInPanel(userName).Render(r.Context(), w)
	}
}

// Interfaces for dependency injection
type UserLister interface {
	ListUsers() ([]vinyl.User, error)
}

type UserGetter interface {
	GetUserByID(userID int64) (*vinyl.User, error)
}

type UserCreator interface {
	CreateUser(name string) (vinyl.User, error)
}
