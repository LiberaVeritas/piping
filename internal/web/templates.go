package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

func StaticHandler() http.Handler {
	return http.FileServerFS(staticFS)
}

type renderer = *template.Template

func parsePages() (map[string]renderer, error) {
	pages := map[string]renderer{}
	for _, name := range []string{"home", "jobs", "result", "admin"} {
		layout, err := template.ParseFS(templateFS, "templates/layout.html")
		if err != nil {
			return nil, fmt.Errorf("parsing layout: %w", err)
		}
		t, err := layout.ParseFS(templateFS, "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parsing %q: %w", name, err)
		}
		pages[name] = t
	}
	return pages, nil
}

func (s *Server) render(w http.ResponseWriter, status int, page string, data any) {
	t, ok := s.pages[page]
	if !ok {
		s.log.Error("render: unknown page", "page", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	root := "layout"

	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, root, data); err != nil {
		s.log.Error("render", "page", page, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}
