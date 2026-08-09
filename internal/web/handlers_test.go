package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"piping/internal/app"
	"piping/internal/job"
	"piping/internal/oidc"
	"piping/internal/pdf"
	"piping/internal/queue"
	"piping/internal/quota"
	"piping/internal/seal"
	"piping/internal/session"
	"piping/internal/user"
)

// --- fakes for the collaborators the handlers reach through ---

type fakeDash struct {
	remaining, granted int
	queues             []queue.Queue
	jobs               []job.WithDestinationName
	err                error
}

func (f *fakeDash) RemainingQuota(context.Context, string) (int, error) {
	return f.remaining, f.err
}
func (f *fakeDash) GrantedQuota(context.Context, string) (int, error) {
	return f.granted, f.err
}
func (f *fakeDash) EnabledQueues(context.Context) ([]queue.Queue, error) {
	return f.queues, f.err
}
func (f *fakeDash) JobsWithDestinationForUser(context.Context, string,
	time.Time, int) ([]job.WithDestinationName, error) {
	return f.jobs, f.err
}

type webAnalyzer struct {
	pages, colorPages int
	err               error
}

func (f *webAnalyzer) CountPages(context.Context, []byte) (int, int, error) {
	return f.pages, f.colorPages, f.err
}

type webQueueReader struct {
	q   queue.Queue
	err error
}

func (f *webQueueReader) GetQueue(context.Context, int64) (queue.Queue, error) {
	return f.q, f.err
}

type webQuotaStore struct {
	err   error
	calls int
}

func (f *webQuotaStore) CheckQuotaAndStore(_ context.Context, j job.Job) (job.Job, error) {
	f.calls++
	if f.err != nil {
		return job.Job{ID: 1}, f.err
	}
	j.ID = 1
	j.State = job.QuotaDeducted
	return j, nil
}

type webDeliverer struct {
	outcome app.DeliveryOutcome
	err     error
}

func (f *webDeliverer) Deliver(context.Context, job.Job, []byte) (app.DeliveryOutcome, error) {
	return f.outcome, f.err
}

type noopProvStore struct{}

func (noopProvStore) EnsureUser(context.Context, string) error                 { return nil }
func (noopProvStore) EnsureSemester(_ context.Context, _, dq int) (int, error) { return dq, nil }
func (noopProvStore) EnsureGrant(context.Context, string, int, int) error      { return nil }

// --- test server ---

type testDeps struct {
	analyzer *webAnalyzer
	queues   *webQueueReader
	jobs     *webQuotaStore
	deliver  *webDeliverer
	dash     *fakeDash
	ready    func(context.Context) error
	sessions *session.Manager
}

func newDeps() *testDeps {
	return &testDeps{
		analyzer: &webAnalyzer{pages: 3},
		queues:   &webQueueReader{q: queue.Queue{ID: 1, Name: "Trottier", Enabled: true, Policy: queue.UniformPolicy}},
		jobs:     &webQuotaStore{},
		deliver:  &webDeliverer{outcome: app.DeliverySucceeded},
		dash:     &fakeDash{remaining: 240, granted: 250},
		ready:    func(context.Context) error { return nil },
	}
}

const (
	// what main.go derives from OIDC_REDIRECT_URI: scheme://host
	testOrigin = "https://piping.test"
	// what a browser sends for a request from our own page
	sameOrigin = "same-origin"
)

func (d *testDeps) handler(t *testing.T) http.Handler {
	t.Helper()
	log := slog.New(slog.DiscardHandler)

	sealer, err := seal.NewSealer(bytes.Repeat([]byte("k"), 32))
	if err != nil {
		t.Fatal(err)
	}
	d.sessions = session.NewManager(sealer, time.Hour, log)

	oc, err := oidc.New(context.Background(), oidc.ClientConfig{
		RedirectURI: "https://piping.test/auth/callback",
		ClientID:    "id", ClientSecret: "secret", Scopes: "openid",
		Sealer: sealer, Log: log,
	}, "https://idp.test/authorize", "https://idp.test/token", "https://idp.test/userinfo")
	if err != nil {
		t.Fatal(err)
	}

	submitter := app.NewSubmitter(d.analyzer, d.queues, d.jobs, d.deliver,
		quota.Rates{ColorRate: 5}, 1<<20, 100, 10, log)

	srv, err := NewServer(submitter, app.NewProvisioner(noopProvStore{}, 250, log),
		d.dash, oc, d.sessions, d.ready, 1<<20, 10, testOrigin, "test-build", log)
	if err != nil {
		t.Fatal(err)
	}
	return srv.Routes()
}

func (d *testDeps) authed(t *testing.T, role user.Role, req *http.Request) *http.Request {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := d.sessions.Issue(rec, "testuser", role); err != nil {
		t.Fatal(err)
	}
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	return req
}

func multipartBody(t *testing.T, filename string, content []byte, fields map[string]string) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if filename != "" {
		fw, err := w.CreateFormFile("document", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func (d *testDeps) submitRequest(t *testing.T, filename string, content []byte, fields map[string]string) *http.Request {
	t.Helper()
	body, contentType := multipartBody(t, filename, content, fields)
	req := httptest.NewRequest(http.MethodPost, "/job", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Sec-Fetch-Site", sameOrigin)
	return d.authed(t, user.RoleUser, req)
}

func validFields() map[string]string {
	return map[string]string{"queue_id": "1", "copies": "1"}
}

// --- authentication and authorisation ---

func TestApplicationRoutesRequireASession(t *testing.T) {
	d := newDeps()
	h := d.handler(t)

	for _, target := range []string{"/", "/jobs", "/admin"} {
		t.Run(target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))

			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want %d (redirect to the IdP)", rec.Code, http.StatusSeeOther)
			}
			if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "https://idp.test/authorize") {
				t.Errorf("Location = %q, want the IdP authorize endpoint", loc)
			}
			if strings.Contains(rec.Body.String(), "testuser") {
				t.Error("unauthenticated response leaked page content")
			}
		})
	}
}

func TestSubmitWithoutASessionIsNotProcessed(t *testing.T) {
	d := newDeps()
	h := d.handler(t)

	body, contentType := multipartBody(t, "doc.pdf", []byte("%PDF-1.4\n"), validFields())
	req := httptest.NewRequest(http.MethodPost, "/job", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Sec-Fetch-Site", sameOrigin)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Error("an unauthenticated upload was accepted")
	}
	if d.jobs.calls != 0 {
		t.Error("an unauthenticated upload reached the quota store")
	}
}

func TestAdminRequiresStaff(t *testing.T) {
	cases := []struct {
		role user.Role
		want int
	}{
		{user.RoleNone, http.StatusForbidden},
		{user.RoleUser, http.StatusForbidden},
		{user.RoleStaff, http.StatusOK},
		{user.RoleAdmin, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.role.String(), func(t *testing.T) {
			d := newDeps()
			h := d.handler(t)
			req := d.authed(t, tc.role, httptest.NewRequest(http.MethodGet, "/admin", nil))

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Errorf("role %q got status %d, want %d", tc.role, rec.Code, tc.want)
			}
		})
	}
}

// the only CSRF defence on the upload endpoint
func TestSubmitRequiresExpectedFetchSite(t *testing.T) {
	for _, site := range []string{"", "cross-site", "same-site"} {
		t.Run("Sec-Fetch-Site: "+site, func(t *testing.T) {
			d := newDeps()
			h := d.handler(t)
			req := d.submitRequest(t, "doc.pdf", []byte("%PDF-1.4\n"), validFields())
			if site == "" {
				req.Header.Del("Sec-Fetch-Site")
			} else {
				req.Header.Set("Sec-Fetch-Site", site)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
			if d.jobs.calls != 0 {
				t.Error("a cross-origin upload reached the quota store")
			}
		})
	}
}

// second layer: Sec-Fetch-Site says same-origin, but the Origin header — when
// the browser sends one — has to be ours too.
func TestSubmitChecksOriginHeaderWhenPresent(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		want   int
	}{
		{"matching origin", testOrigin, http.StatusOK},
		{"foreign origin", "https://evil.test", http.StatusForbidden},
		{"origin absent", "", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newDeps()
			h := d.handler(t)
			req := d.submitRequest(t, "doc.pdf", []byte("%PDF-1.4\n"), validFields())
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
			if stored := d.jobs.calls > 0; stored != (tc.want == http.StatusOK) {
				t.Errorf("reached the quota store = %v, want %v", stored, tc.want == http.StatusOK)
			}
		})
	}
}

func TestJobEndpointRejectsNonPost(t *testing.T) {
	d := newDeps()
	h := d.handler(t)
	req := d.authed(t, user.RoleUser, httptest.NewRequest(http.MethodGet, "/job", nil))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /job status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

// --- unauthenticated routes ---

func TestPublicRoutesNeedNoSession(t *testing.T) {
	d := newDeps()
	h := d.handler(t)

	// static assets must not redirect, or the login page loses its styling
	for _, target := range []string{"/healthz", "/readyz", "/static/piping.css"} {
		t.Run(target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
			}
		})
	}
}

func TestReadyzReflectsTheProbe(t *testing.T) {
	cases := []struct {
		name  string
		probe func(context.Context) error
		want  int
	}{
		{"healthy", func(context.Context) error { return nil }, http.StatusOK},
		{"failing", func(context.Context) error { return errors.New("db down") }, http.StatusServiceUnavailable},
		{"unset", nil, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newDeps()
			d.ready = tc.probe
			h := d.handler(t)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

// --- pages ---

func TestHomeRendersTheUsersDashboard(t *testing.T) {
	d := newDeps()
	d.dash.queues = []queue.Queue{{ID: 1, Name: "Trottier", Enabled: true}}
	d.dash.jobs = []job.WithDestinationName{{
		Job: job.Job{ID: 1, DocumentName: "essay.pdf", NumPages: 3, Copies: 1,
			Cost: 3, State: job.PrintSucceeded, SubmittedAt: time.Now()},
		DestinationName: "Trottier 3rd",
	}}
	h := d.handler(t)
	req := d.authed(t, user.RoleUser, httptest.NewRequest(http.MethodGet, "/", nil))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"testuser", "240", "Trottier", "essay.pdf"} {
		if !strings.Contains(body, want) {
			t.Errorf("home page does not mention %q", want)
		}
	}
}

func TestDashboardFailureIsAnErrorNotAPartialPage(t *testing.T) {
	for _, target := range []string{"/", "/jobs"} {
		t.Run(target, func(t *testing.T) {
			d := newDeps()
			d.dash.err = errors.New("db down")
			h := d.handler(t)
			req := d.authed(t, user.RoleUser, httptest.NewRequest(http.MethodGet, target, nil))

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
			}
			if strings.Contains(rec.Body.String(), "db down") {
				t.Error("internal error text was shown to the user")
			}
		})
	}
}

// --- upload handling ---

func TestSubmitFormValidation(t *testing.T) {
	pdfBytes := []byte("%PDF-1.4\n")
	cases := []struct {
		name     string
		filename string
		content  []byte
		fields   map[string]string
		want     int
		stored   bool
	}{
		{"accepted", "doc.pdf", pdfBytes, validFields(), http.StatusOK, true},
		{"no document attached", "", nil, validFields(), http.StatusBadRequest, false},
		{"queue id missing", "doc.pdf", pdfBytes, map[string]string{"copies": "1"}, http.StatusBadRequest, false},
		{"queue id not a number", "doc.pdf", pdfBytes,
			map[string]string{"queue_id": "abc", "copies": "1"}, http.StatusBadRequest, false},
		{"copies not a number", "doc.pdf", pdfBytes,
			map[string]string{"queue_id": "1", "copies": "many"}, http.StatusBadRequest, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newDeps()
			h := d.handler(t)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, d.submitRequest(t, tc.filename, tc.content, tc.fields))

			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
			if stored := d.jobs.calls > 0; stored != tc.stored {
				t.Errorf("reached the quota store = %v, want %v", stored, tc.stored)
			}
		})
	}
}

func TestSubmitReportsRejectionsToTheUser(t *testing.T) {
	d := newDeps()
	h := d.handler(t)

	// a non-PDF upload must come back as an explanation, not a 500
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, d.submitRequest(t, "notes.txt", []byte("just some bytes"), validFields()))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if !strings.Contains(rec.Body.String(), "not a PDF") {
		t.Errorf("body does not explain the rejection: %s", rec.Body.String())
	}
	if d.jobs.calls != 0 {
		t.Error("a non-PDF reached the quota store")
	}
}

func TestSubmitReportsFailedDeliveryAsRefunded(t *testing.T) {
	d := newDeps()
	d.deliver.outcome = app.DeliveryFailed
	h := d.handler(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, d.submitRequest(t, "doc.pdf", []byte("%PDF-1.4\n"), validFields()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "refunded") {
		t.Errorf("a failed print must tell the user their quota came back: %s", rec.Body.String())
	}
}

// htmx only swaps successful responses into the page, so an error result has
// to come back as 200 or the user sees nothing change.
func TestHTMXResultsAreAlwaysOK(t *testing.T) {
	d := newDeps()
	h := d.handler(t)
	req := d.submitRequest(t, "notes.txt", []byte("just some bytes"), validFields())
	req.Header.Set("HX-Request", "true")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for an htmx request", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not a PDF") {
		t.Error("htmx response lost the explanation")
	}
}

// --- pure helpers ---

func TestMapSubmitError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not a pdf", pdf.ErrNotPDF, http.StatusUnprocessableEntity},
		{"unreadable pdf", pdf.ErrUnreadable, http.StatusUnprocessableEntity},
		{"too large", app.ErrTooLarge, http.StatusRequestEntityTooLarge},
		{"too many pages", app.ErrTooManyPages, http.StatusUnprocessableEntity},
		{"invalid copies", app.ErrInvalidCopies, http.StatusUnprocessableEntity},
		{"queue unavailable", queue.ErrUnavailable, http.StatusConflict},
		{"insufficient quota", quota.ErrInsufficient, http.StatusConflict},
		{"unknown", errors.New("connection reset by peer"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// wrapped the way the app layer returns them
			status, msg := mapSubmitError(errors.Join(errors.New("submitting job 1"), tc.err))
			if status != tc.want {
				t.Errorf("status = %d, want %d", status, tc.want)
			}
			if msg == "" {
				t.Error("no message for the user")
			}
			if strings.Contains(msg, tc.err.Error()) && tc.want == http.StatusInternalServerError {
				t.Errorf("message leaks the internal error: %q", msg)
			}
		})
	}
}

// the post-login redirect target comes back from the IdP round trip, so it
// must never be able to send the user off-site.
func TestSafePath(t *testing.T) {
	cases := map[string]string{
		"/jobs":                    "/jobs",
		"/jobs?queue=1":            "/jobs?queue=1",
		"/":                        "/",
		"":                         "/",
		"jobs":                     "/",
		"//evil.test/x":            "/",
		"https://evil.test/x":      "/",
		"http://evil.test":         "/",
		"javascript:alert(1)":      "/",
		"\\\\evil.test":            "/",
		"https://user@evil.test/x": "/",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := safePath(in); got != want {
				t.Errorf("safePath(%q) = %q, want %q", in, got, want)
			}
		})
	}
}
