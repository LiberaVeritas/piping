package session

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"piping/internal/user"
)

const (
	sessionLabel = "session"
	cookieName   = "piping_session"
)

var ErrExpired = errors.New("session expired")

type Session struct {
	Sub  string    `json:"sub"`
	Role user.Role `json:"role"`
	Exp  int64     `json:"exp"`
}

type Sealer interface {
	SealAsJSON(label string, session any) (string, error)
	OpenAsJSON(label, cookie string, session any) error
}

type Manager struct {
	seal Sealer
	ttl  time.Duration
	log  *slog.Logger
}

func NewManager(seal Sealer, ttl time.Duration, log *slog.Logger) *Manager {
	return &Manager{
		seal: seal,
		ttl:  ttl,
		log:  log,
	}
}

func (m *Manager) FromRequest(r *http.Request) (Session, error) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return Session{}, fmt.Errorf("getting cookie %s: %w", cookieName, err)
	}
	var session Session
	err = m.seal.OpenAsJSON(sessionLabel, cookie.Value, &session)
	if err != nil {
		return Session{}, fmt.Errorf("invalid session: %w", err)
	}
	if time.Now().Unix() > session.Exp {
		return Session{}, fmt.Errorf("%w for user %q", ErrExpired, session.Sub)
	}
	return session, nil
}

func (m *Manager) Issue(w http.ResponseWriter, sub string, role user.Role) error {
	m.log.Debug("issuing session cookie", "user", sub, "role", role)
	sealed, err := m.seal.SealAsJSON(sessionLabel, Session{
		Sub:  sub,
		Role: role,
		Exp:  time.Now().Add(m.ttl).Unix(),
	})
	if err != nil {
		return fmt.Errorf("sealing session: %w", err)
	}
	// TODO support DBSC
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    sealed,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(m.ttl.Seconds()),
	})
	m.log.Info("Issued session", "user", sub, "role", role)
	return nil
}

func (m *Manager) Clear(w http.ResponseWriter) {
	m.log.Debug("clearing session cookie")
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
