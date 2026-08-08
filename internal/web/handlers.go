package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"piping/internal/app"
	"piping/internal/pdf"
	"piping/internal/queue"
	"piping/internal/quota"
	"piping/internal/semester"
)

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	username := sessionFrom(r.Context()).Sub
	remaining, err := s.dash.RemainingQuota(r.Context(), username)
	if err != nil {
		s.log.Error("home: remaining quota", "user", username, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	granted, err := s.dash.GrantedQuota(r.Context(), username)
	if err != nil {
		s.log.Error("home: granted quota", "user", username, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	queues, err := s.dash.EnabledQueues(r.Context())
	if err != nil {
		s.log.Error("home: queues", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	recent, err := s.dash.JobsWithDestinationForUser(r.Context(), username, time.Now().Add(-24*time.Hour), 5)
	if err != nil {
		s.log.Error("home: recent jobs", "user", username, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	s.render(w, http.StatusOK, "home", homeView{
		baseView:  s.base(r.Context()),
		Remaining: remaining,
		Granted:   granted,
		Queues:    toQueueViews(queues),
		Recent:    toJobViews(recent),
		MaxCopies: s.maxCopies,
		MaxSize:   s.maxBytes,
	})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	username := sessionFrom(r.Context()).Sub
	jobs, err := s.dash.JobsWithDestinationForUser(r.Context(), username, time.Unix(0, 0), 50)
	if err != nil {
		s.log.Error("jobs list", "user", username, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, http.StatusOK, "jobs", jobsView{
		baseView: s.base(r.Context()),
		Jobs:     toJobViews(jobs),
	})
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "admin", adminView{baseView: s.base(r.Context())})
}

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBytes+(1<<20)) // + form overhead
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		s.result(w, r, http.StatusRequestEntityTooLarge, "Not printed", "Upload too large or malformed.")
		return
	}
	file, hdr, err := r.FormFile("document")
	if err != nil {
		s.result(w, r, http.StatusBadRequest, "Not printed", "No document attached.")
		return
	}
	doc, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil {
		s.result(w, r, http.StatusBadRequest, "Not printed", "Could not read the upload.")
		return
	}
	queueID, err := strconv.ParseInt(r.FormValue("queue_id"), 10, 64)
	if err != nil {
		s.result(w, r, http.StatusBadRequest, "Not printed", "Choose a printer.")
		return
	}
	copies, err := strconv.Atoi(r.FormValue("copies"))
	if err != nil {
		s.result(w, r, http.StatusBadRequest, "Not printed", "Invalid copy count.")
		return
	}

	res, err := s.submit.Submit(r.Context(), app.SubmitInput{
		Username: sessionFrom(r.Context()).Sub,
		QueueID:  queueID,
		Document: doc,
		Color:    r.FormValue("color") == "on",
		Duplex:   r.FormValue("duplex") == "on",
		Copies:   copies,
		Filename: hdr.Filename,
	})
	if err != nil {
		status, msg := mapSubmitError(err)
		s.result(w, r, status, "Not printed", msg)
		return
	}
	if res.Outcome == app.DeliverySucceeded {
		s.result(w, r, http.StatusOK, "Sent to printer",
			fmt.Sprintf("%d page(s), %d quota deducted.", res.Pages, res.Cost))
		return
	}
	// Non-success means print_failed with quota back by the time we respond.
	s.result(w, r, http.StatusOK, "Printing failed",
		"The job could not be printed and your quota was refunded. Please try again.")
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
	s.log.Debug("handling auth callback", "method", r.Method, "url", r.URL)
	query := r.URL.Query()
	if !query.Has("code") || !query.Has("state") {
		s.log.Info("auth callback req", "has code", query.Has("code"), "has state", query.Has("state"))
		http.Error(w, "server error while logging in", http.StatusBadRequest)
		return
	}
	userInfo, originalURL, err := s.oidc.GetUserInfo(r.Context(), query.Get("code"), query.Get("state"))
	if err != nil {
		s.log.Info("handling auth callback", "err", err)
		http.Error(w, "server error while logging in", http.StatusBadRequest)
		return
	}
	u := s.oidc.ToUser(userInfo)
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
	s.log.Debug("redirecting to original url", "url", originalURL)
	// #nosec G710
	http.Redirect(w, r, safePath(originalURL), http.StatusSeeOther)
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if s.ready == nil || s.ready(ctx) != nil {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	_, _ = io.WriteString(w, "ready\n")
}

func (s *Server) result(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	if r.Header.Get("HX-Request") == "true" {
		// Fragment for the htmx swap target. Always 200: htmx's default is
		// to swap only 2xx responses, and the fragment TEXT carries the
		// error; the no-JS path below keeps honest status codes.
		s.render(w, http.StatusOK, "result",
			resultView{Title: title, Message: message})
		return
	}
	s.render(w, status, "result", resultView{baseView: s.base(r.Context()), Title: title, Message: message})
}

// mapSubmitError: the complete sentinel -> (status, user message) table. The
// handler's error policy in one place; details go to the log, never the user.
func mapSubmitError(err error) (int, string) {
	switch {
	case errors.Is(err, pdf.ErrNotPDF):
		return http.StatusUnprocessableEntity,
			"That file is not a PDF. Export your document as PDF and try again."
	case errors.Is(err, pdf.ErrUnreadable):
		return http.StatusUnprocessableEntity,
			"That PDF could not be read — it may be corrupt or password-protected."
	case errors.Is(err, app.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, "The document exceeds the size limit."
	case errors.Is(err, app.ErrTooManyPages):
		return http.StatusUnprocessableEntity, "The document exceeds the page limit."
	case errors.Is(err, app.ErrInvalidCopies):
		return http.StatusUnprocessableEntity, "The copy count is out of range."
	case errors.Is(err, queue.ErrUnavailable):
		return http.StatusConflict, "That printer is currently unavailable. Choose another."
	case errors.Is(err, quota.ErrInsufficient):
		return http.StatusConflict, "You do not have enough quota for this job."
	default:
		return http.StatusInternalServerError,
			"Something went wrong and we could not confirm your job's status. Check your job history before submitting again."
	}
}
