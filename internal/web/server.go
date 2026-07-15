// TODO
package web

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"piping/internal/app"
)

type Server struct {
	submit    *app.Submitter
	prov      *app.Provisioner
	ready     func(context.Context) error
	maxUpload int64
	log       *slog.Logger
}

func NewServer(submit *app.Submitter, prov *app.Provisioner,
	ready func(context.Context) error, maxUpload int64, log *slog.Logger) *Server {
	return &Server{
		submit: submit, prov: prov, ready: ready,
		maxUpload: maxUpload, log: log,
	}
}

func (s *Server) Routes() http.Handler {
	appMux := http.NewServeMux()
	appMux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "Hello world")
	})

	root := http.NewServeMux()
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
	root.Handle("/", appMux)
	return root
}

