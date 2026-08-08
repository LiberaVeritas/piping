package session_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pgregory.net/rapid"

	"piping/internal/seal"
	"piping/internal/session"
	"piping/internal/user"
)

func newSealer() *seal.Sealer {
	s, err := seal.NewSealer([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		panic(err)
	}
	return s
}

func manager(t *testing.T, ttl time.Duration) *session.Manager {
	t.Helper()
	return session.NewManager(newSealer(), ttl, slog.Default())
}

func managerRapid(rt *rapid.T) *session.Manager {
	ttl := time.Duration(rapid.IntRange(1, 86400).Draw(rt, "ttlSec")) * time.Second
	rt.Helper()
	s := newSealer()
	return session.NewManager(s, ttl, slog.Default())
}

func requestWithCookies(w *httptest.ResponseRecorder) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}
	return r
}

func request(t *testing.T, w *httptest.ResponseRecorder) *http.Request {
	t.Helper()
	return requestWithCookies(w)
}

func TestSessionRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		m := managerRapid(rt)
		w := httptest.NewRecorder()
		sub := rapid.String().Draw(rt, "sub")
		role := rapid.SampledFrom([]user.Role{
			user.RoleNone, user.RoleUser, user.RoleStaff, user.RoleAdmin,
		}).Draw(rt, "role")
		if err := m.Issue(w, sub, role); err != nil {
			rt.Fatal(err)
		}
		sess, err := m.FromRequest(requestWithCookies(w))
		if err != nil {
			rt.Fatalf("issued session failed to open (sub=%q role=%v): %v", sub, role, err)
		}
		if sess.Sub != sub || sess.Role != role {
			rt.Fatalf("round trip: (%q,%v) -> (%q,%v)", sub, role, sess.Sub, sess.Role)
		}
	})
}

func TestCookieSecurityAttributes(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		m := managerRapid(rt)
		w := httptest.NewRecorder()
		sub := rapid.String().Draw(rt, "sub")
		role := rapid.SampledFrom([]user.Role{
			user.RoleNone, user.RoleUser, user.RoleStaff, user.RoleAdmin,
		}).Draw(rt, "role")

		if err := m.Issue(w, sub, role); err != nil {
			rt.Fatal(err)
		}

		if c := w.Result().Cookies()[0]; !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
			rt.Fatalf("cookie missing security attributes: %+v", c)
		}
	})
}

func TestRoleNoneSessionRoundTrips(t *testing.T) {
	m := manager(t, time.Hour)
	w := httptest.NewRecorder()

	if err := m.Issue(w, "student1", user.RoleNone); err != nil {
		t.Fatal(err)
	}
	sess, err := m.FromRequest(request(t, w))
	if err != nil {
		t.Fatalf("RoleNone didn't create sesion; will lead to redirect loop: %v", err)
	}
	if sess.Role != user.RoleNone {
		t.Errorf("role = %v, want RoleNone", sess.Role)
	}
}

func TestExpiredRejected(t *testing.T) {
	m := manager(t, -time.Minute)
	w := httptest.NewRecorder()
	_ = m.Issue(w, "testuser", user.RoleNone)

	if _, err := m.FromRequest(request(t, w)); err == nil {
		t.Fatal("expired session not rejected")
	}
}

func TestNoCookieRejected(t *testing.T) {
	m := manager(t, time.Hour)

	if _, err := m.FromRequest(httptest.NewRequest("GET", "/", nil)); err == nil {
		t.Fatal("no cookie but not rejected")
	}
}

func TestStateBlobRejectedAsSession(t *testing.T) {
	s, _ := seal.NewSealer([]byte("0123456789abcdef0123456789abcdef"))
	m := session.NewManager(s, time.Hour, slog.Default())
	stateBlob, _ := s.SealAsJSON("oidc_state", map[string]string{"original_url": "/", "pkce_verifier": "x"})
	r := httptest.NewRequest("GET", "/", nil)
	// #nosec G124
	r.AddCookie(&http.Cookie{Name: "piping_session", Value: stateBlob})

	if _, err := m.FromRequest(r); err == nil {
		t.Fatal("sealed state accepted as session")
	}
}

func TestClearDeletes(t *testing.T) {
	m := manager(t, time.Hour)
	w := httptest.NewRecorder()
	m.Clear(w)
	c := w.Result().Cookies()[0]
	if c.MaxAge != -1 || c.Value != "" {
		t.Errorf("Clear must set MaxAge=-1 empty value, got %+v", c)
	}
}
