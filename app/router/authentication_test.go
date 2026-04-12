package router

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ninesl/vinyl-keeper/app/router/values"
	"github.com/ninesl/vinyl-keeper/app/vinyl"
)

type fakeUserManager struct {
	users         []vinyl.User
	deletedUserID int64
}

func (f *fakeUserManager) ListUsers() ([]vinyl.User, error) {
	return append([]vinyl.User(nil), f.users...), nil
}

func (f *fakeUserManager) CreateUser(name string) (vinyl.User, error) {
	user := vinyl.User{UserID: int64(len(f.users) + 1), UserName: name}
	f.users = append(f.users, user)
	return user, nil
}

func (f *fakeUserManager) DeleteUser(userID int64) error {
	f.deletedUserID = userID
	kept := make([]vinyl.User, 0, len(f.users))
	for _, user := range f.users {
		if user.UserID != userID {
			kept = append(kept, user)
		}
	}
	f.users = kept
	return nil
}

func TestSignInCreateUserHandler_FirstUserRefreshesList(t *testing.T) {
	fake := &fakeUserManager{}
	handler := SignInCreateUserHandler(fake)

	form := url.Values{}
	form.Set(values.QueryUserName, "alice")
	req := httptest.NewRequest(http.MethodPost, values.EndpointSignIn+values.EndpointNew, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "alice") {
		t.Fatalf("expected created user to be rendered, got body: %s", body)
	}
	if strings.Contains(body, "No users yet") {
		t.Fatalf("expected empty-state message to be removed after create, got body: %s", body)
	}
}

func TestSignInDeleteUserHandler_DeletesAndRefreshesList(t *testing.T) {
	fake := &fakeUserManager{users: []vinyl.User{{UserID: 1, UserName: "alice"}, {UserID: 2, UserName: "bob"}}}
	handler := SignInDeleteUserHandler(fake)

	form := url.Values{}
	form.Set(values.QueryUserID, "1")
	req := httptest.NewRequest(http.MethodPost, values.EndpointSignIn+values.EndpointUserDelete, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if fake.deletedUserID != 1 {
		t.Fatalf("expected delete to be called with userID=1, got %d", fake.deletedUserID)
	}
	body := w.Body.String()
	if strings.Contains(body, "alice") {
		t.Fatalf("expected deleted user to be removed from rendered list, got body: %s", body)
	}
	if !strings.Contains(body, "bob") {
		t.Fatalf("expected remaining users to be rendered, got body: %s", body)
	}
}
