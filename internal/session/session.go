package session

import (
	"fmt"
	"net/http"
	"time"

	"piping/internal/user"
)

const (
	sessionLabel = "session"
	cookieName   = "piping_session"
)

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
}

func NewManager(seal Sealer, ttl time.Duration) *Manager {
	return &Manager{
		seal: seal,
		ttl:  ttl,
	}
}

func (m *Manager) FromRequest(r *http.Request) (Session, error) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return Session{}, err
	}
	var session Session
	err = m.seal.OpenAsJSON(sessionLabel, cookie.Value, &session)
	if err != nil {
		return Session{}, fmt.Errorf("invalid session: %w", err)
	}
	if time.Now().Unix() > session.Exp {
		return Session{}, fmt.Errorf("session expired")
	}
	return session, nil
}

func (m *Manager) Issue(w http.ResponseWriter, sub string, role user.Role) error {
	sealed, err := m.seal.SealAsJSON(sessionLabel, Session{
		Sub:  sub,
		Role: role,
		Exp:  time.Now().Add(m.ttl).Unix(),
	})
	if err != nil {
		return fmt.Errorf("sealing session: %w", err)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    sealed,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(m.ttl.Seconds()),
	})
	return nil
}
