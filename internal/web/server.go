package web

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"piping/internal/app"
	"piping/internal/oidc"
	"piping/internal/semester"
	"piping/internal/session"
	"piping/internal/user"
)

type Server struct {
	submit    *app.Submitter
	prov      *app.Provisioner
	oidc      *oidc.Client
	session   *session.Manager
	ready     func(context.Context) error
	maxUpload int64
	log       *slog.Logger
}

func NewServer(submit *app.Submitter, prov *app.Provisioner, oidc *oidc.Client, session *session.Manager,
	ready func(context.Context) error, maxUpload int64, log *slog.Logger) *Server {
	return &Server{
		submit: submit, prov: prov, oidc: oidc, session: session,
		ready: ready, maxUpload: maxUpload, log: log,
	}
}

func (s *Server) Routes() http.Handler {
	appMux := http.NewServeMux()
	appMux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "Hello world")
	})
	root := http.NewServeMux()
	root.HandleFunc("GET /auth/callback", s.handleAuthCallback)
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	})
	root.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if s.ready == nil || s.ready(ctx) != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ready\n")
	})
	root.Handle("/", s.requireSession(appMux))
	return root
}

type sessionKey struct{}

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.session.FromRequest(r)
		if err != nil {
			authURL, aErr := s.oidc.GetAuthURL(r.URL.RequestURI())
			if aErr != nil {
				s.log.Error("logging in", "auth err", aErr, "session err", err)
				http.Error(w, "server error while logging in", http.StatusInternalServerError)
				return
			}
			// #nosec G710
			http.Redirect(w, r, authURL, http.StatusSeeOther)
			return
		}
		reqCtx := context.WithValue(r.Context(), sessionKey{}, sess)
		next.ServeHTTP(w, r.WithContext(reqCtx))
	})
}

func (s *Server) requireRole(requiredRole user.Role, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		if sess := r.Context().Value(sessionKey{}).(session.Session); user.RoleRank(sess.Role) < user.RoleRank(requiredRole) {
			s.log.Info("unprivileged access attempt", "user", sess.Sub,
				"role", sess.Role, "path", r.URL.RequestURI())
			http.Error(w, "you do not have the required permissions", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

func safePath(p string) string {
	u, err := url.Parse(p)
	if err != nil {
		return "/"
	}
	if u.IsAbs() || u.Host != "" {
		return "/"
	}
	if !strings.HasPrefix(u.Path, "/") {
		return "/"
	}
	return u.RequestURI()
}

func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if !query.Has("code") || !query.Has("state") {
		s.log.Info("auth callback req missing code or state", "query", query)
		http.Error(w, "server error while logging in", http.StatusBadRequest)
		return
	}
	userInfo, originalURL, err := s.oidc.GetUserInfo(r.Context(), query.Get("code"), query.Get("state"))
	if err != nil {
		s.log.Info("handling auth callback", "err", err)
		http.Error(w, "server error while logging in", http.StatusBadRequest)
		return
	}
	u, err := userInfo.ToUser()
	if err != nil {
		s.log.Error("parsing user info", "err", err)
		http.Error(w, "server error while logging in", http.StatusInternalServerError)
		return
	}
	err = s.prov.Provision(r.Context(), u, semester.Current(time.Now()))
	if err != nil {
		s.log.Error("provisioning upon login", "err", err)
		http.Error(w, "server error while logging in", http.StatusInternalServerError)
		return
	}
	err = s.session.Issue(w, u.Username, u.Role)
	if err != nil {
		s.log.Error("issuing session cookie", "err", err)
		http.Error(w, "server error while logging in", http.StatusInternalServerError)
		return
	}
	// #nosec G710
	http.Redirect(w, r, safePath(originalURL), http.StatusSeeOther)
}
