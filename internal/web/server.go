package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"piping/internal/app"
	"piping/internal/job"
	"piping/internal/oidc"
	"piping/internal/queue"
	"piping/internal/session"
	"piping/internal/user"
)

type dashboardStore interface {
	RemainingQuota(ctx context.Context, username string) (int, error)
	GrantedQuota(ctx context.Context, username string) (int, error)
	EnabledQueues(ctx context.Context) ([]queue.Queue, error)
	JobsWithDestinationForUser(ctx context.Context, username string,
		newerThan time.Time, limit int) ([]job.WithDestinationName, error)
}

type Server struct {
	submit    *app.Submitter
	prov      *app.Provisioner
	dash      dashboardStore
	oidc      *oidc.Client
	session   *session.Manager
	ready     func(context.Context) error
	maxBytes  int64
	maxCopies int
	origin    string
	log       *slog.Logger
	pages     map[string]renderer
}

func NewServer(submit *app.Submitter, prov *app.Provisioner, dash dashboardStore,
	oidc *oidc.Client, sess *session.Manager, ready func(context.Context) error,
	maxUpload int64, maxCopies int, origin string, log *slog.Logger) (*Server, error) {
	pages, err := parsePages()
	if err != nil {
		return nil, err
	}
	return &Server{
		submit: submit, prov: prov, dash: dash, oidc: oidc, session: sess, ready: ready,
		maxBytes: maxUpload, maxCopies: maxCopies, origin: origin, log: log, pages: pages,
	}, nil
}

func (s *Server) Routes() http.Handler {
	appMux := http.NewServeMux()
	appMux.HandleFunc("GET /{$}", s.handleHome)
	appMux.Handle("POST /job", s.checkOrigin(s.handleSubmit))
	appMux.HandleFunc("GET /jobs", s.handleJobs)
	// TODO
	appMux.Handle("GET /admin", s.requireRole(user.RoleStaff, s.handleAdmin))

	root := http.NewServeMux()
	root.Handle("GET /static/", StaticHandler())
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	})
	root.HandleFunc("GET /readyz", s.handleReady)
	root.HandleFunc("GET /auth/callback", s.handleAuthCallback)
	root.Handle("/", s.requireSession(appMux))
	return root
}

type sessionKey struct{}

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.log.Debug("require session wrapping", "method", r.Method, "url", r.URL)
		sess, err := s.session.FromRequest(r)
		if err != nil {
			s.log.Info("getting session", "err", err)
			authURL, aErr := s.oidc.GetAuthURL(r.URL.RequestURI())
			if aErr != nil {
				s.log.Error("logging in", "auth err", aErr, "session err", err)
				http.Error(w, "server error while logging in", http.StatusInternalServerError)
				return
			}
			s.log.Debug("no session, redirecting", "auth", authURL)
			// #nosec G710
			http.Redirect(w, r, authURL, http.StatusSeeOther)
			return
		}
		reqCtx := context.WithValue(r.Context(), sessionKey{}, sess)
		next.ServeHTTP(w, r.WithContext(reqCtx))
	})
}

func sessionFrom(ctx context.Context) session.Session {
	sess, _ := ctx.Value(sessionKey{}).(session.Session)
	return sess
}

func (s *Server) requireRole(requiredRole user.Role, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFrom(r.Context())
		if user.RoleRank(sess.Role) < user.RoleRank(requiredRole) {
			s.log.Info("unprivileged access attempt", "user", sess.Sub,
				"role", sess.Role, "path", r.URL.RequestURI())
			http.Error(w, "you do not have the required permissions", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

func (s *Server) checkOrigin(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			s.log.Debug("request received with no origin header set")
			next(w, r)
			return
		}
		if origin != s.origin {
			s.log.Warn("request received from unexpected origin", "origin", origin, "expected", s.origin)
			http.Error(w, "there was an error with your request", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}
