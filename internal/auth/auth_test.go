package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestAuthenticationBootstrapRBACAndRevocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	s, err := New(path, "admin", "placeholder", false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	handler := s.Wrap(next)
	call := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		r.Header.Set("Origin", "http://localhost")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "/api/v1/clusters", "", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call("GET", "/api/v1/health", "", nil); w.Code != 204 {
		t.Fatal(w.Code)
	}
	token, err := os.ReadFile(filepath.Join(filepath.Dir(path), "bootstrap-token"))
	if err != nil {
		t.Fatal(err)
	}
	if w := call("POST", "/api/v1/auth/setup", `{"username":"admin","password":"initial-password-123","token":"wrong"}`, nil); w.Code != 403 {
		t.Fatal(w.Code)
	}
	setup, _ := json.Marshal(map[string]string{"username": "admin", "password": "initial-password-123", "token": strings.TrimSpace(string(token))})
	if w := call("POST", "/api/v1/auth/setup", string(setup), nil); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	if _, err = os.Stat(s.bootstrapPath); !os.IsNotExist(err) {
		t.Fatal("bootstrap token was not removed")
	}
	login := func(username, password string) *http.Cookie {
		body, _ := json.Marshal(map[string]string{"username": username, "password": password})
		w := call("POST", "/api/v1/auth/login", string(body), nil)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		c := w.Result().Cookies()[0]
		if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
			t.Fatal("insecure cookie")
		}
		return c
	}
	admin := login("admin", "initial-password-123")
	for _, role := range []string{"viewer", "operator"} {
		b := `{"username":"` + role + `","password":"test-password-123","role":"` + role + `"}`
		if w := call("POST", "/api/v1/auth/users", b, admin); w.Code != 201 {
			t.Fatal(w.Body.String())
		}
	}
	viewer := login("viewer", "test-password-123")
	operator := login("operator", "test-password-123")
	for _, tc := range []struct {
		method, path string
		c            *http.Cookie
		code         int
	}{
		{"GET", "/api/v1/clusters", viewer, 204}, {"POST", "/api/v1/clusters", viewer, 403}, {"DELETE", "/api/v1/clusters/id", operator, 403}, {"GET", "/api/v1/clusters/id/pods/ns/p/exec", viewer, 403}, {"GET", "/api/v1/clusters/id/pods/ns/p/exec", operator, 204}, {"GET", "/api/v1/auth/users", viewer, 403}, {"POST", "/api/v1/clusters", admin, 204},
	} {
		if w := call(tc.method, tc.path, "", tc.c); w.Code != tc.code {
			t.Fatalf("%s %s=%d expected %d", tc.method, tc.path, w.Code, tc.code)
		}
	}
	bad := httptest.NewRequest("POST", "http://localhost/api/v1/clusters", nil)
	bad.Header.Set("Origin", "http://evil.example")
	bad.AddCookie(admin)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, bad)
	if w.Code != 403 {
		t.Fatal("cross origin accepted")
	}
	if w := call("PATCH", "/api/v1/auth/users", `{"id":1,"role":"viewer"}`, admin); w.Code != 400 {
		t.Fatal("self demotion allowed")
	}
	if w := call("PATCH", "/api/v1/auth/users", `{"id":2,"role":"viewer","disabled":true}`, admin); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := call("GET", "/api/v1/clusters", "", viewer); w.Code != 401 {
		t.Fatal("disabled session accepted")
	}
	if w := call("PUT", "/api/v1/auth/password", `{"current_password":"wrong","password":"new-password-123"}`, operator); w.Code != 403 {
		t.Fatal("wrong current password accepted")
	}
	if w := call("PUT", "/api/v1/auth/password", `{"current_password":"test-password-123","password":"new-password-123"}`, operator); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := call("GET", "/api/v1/clusters", "", operator); w.Code != 401 {
		t.Fatal("old password session accepted")
	}
	operator = login("operator", "new-password-123")
	if w := call("POST", "/api/v1/auth/logout", "", operator); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := call("GET", "/api/v1/clusters", "", operator); w.Code != 401 {
		t.Fatal("logout not revoked")
	}
	// Existing accounts survive a restart; invalid env hash must never overwrite them.
	s2, err := New(path, "admin", "placeholder", false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if s2.bootstrap != "" {
		t.Fatal("bootstrap reopened for existing accounts")
	}
}
func TestLoginRateLimitAndExpiry(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "a.db"), "admin", "bad", true, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := httptest.NewRequest("POST", "http://localhost", nil)
	for i := 0; i < 20; i++ {
		if s.limited(r, "") {
			t.Fatal("limited too early")
		}
	}
	if !s.limited(r, "") {
		t.Fatal("no rate limit")
	}
	// A different source must not inherit the previous source's budget.
	other := httptest.NewRequest("POST", "http://localhost", nil)
	other.RemoteAddr = "203.0.113.9:1234"
	if s.limited(other, "") {
		t.Fatal("unrelated source was limited")
	}
	// The per-account budget must apply across differing source addresses.
	distributed := httptest.NewRequest("POST", "http://localhost", nil)
	for i := 0; i < maxAttemptsPerUser+1; i++ {
		distributed.RemoteAddr = fmt.Sprintf("198.51.100.%d:9000", i)
		blocked := s.limited(distributed, "root")
		if i < maxAttemptsPerUser && blocked {
			t.Fatalf("account limited too early at %d", i)
		}
		if i == maxAttemptsPerUser && !blocked {
			t.Fatal("account brute force across rotating addresses was not limited")
		}
	}
	w := httptest.NewRecorder()
	if err = s.issue(w, User{ID: 1}); err != nil {
		t.Fatal(err)
	}
	if !w.Result().Cookies()[0].Secure {
		t.Fatal("Secure missing")
	}
}

func TestDatabaseSessionIsSharedAcrossServiceInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	hash, err := bcrypt.GenerateFromPassword([]byte("shared-password-123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	first, err := New(path, "admin", string(hash), false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewWithDB(first.db, filepath.Join(filepath.Dir(path), "bootstrap-token"), "admin", string(hash), false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	if err := first.issue(recorder, User{ID: 1}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://localhost/api/v1/clusters", nil)
	request.AddCookie(recorder.Result().Cookies()[0])
	if user, ok := second.current(request); !ok || user.Username != "admin" {
		t.Fatalf("session was not shared: %#v ok=%v", user, ok)
	}
	second.revoke(1)
	if _, ok := first.current(request); ok {
		t.Fatal("revoked shared session is still valid")
	}
}
