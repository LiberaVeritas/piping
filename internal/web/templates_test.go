package web

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func testBase() baseView {
	return baseView{Username: "user", IsStaff: true, Build: "123"}
}

func testJobView() jobView {
	return jobView{
		SubmittedAt: time.Now().Add(-time.Hour), TimeSince: "1h0m",
		DocumentName: "doc.pdf", Pages: 3, Copies: 2, Cost: 6,
		State: "print_succeeded", Destination: "Trottier 3rd",
	}
}

func TestPagesParse(t *testing.T) {
	pages, err := parsePages()
	if err != nil {
		t.Fatalf("parsePages: %v", err)
	}
	for _, name := range []string{"home", "jobs", "result", "admin"} {
		if pages[name] == nil {
			t.Errorf("page %q missing", name)
		}
	}
	if len(pages) != 4 {
		t.Errorf("parsed %d pages, want 4", len(pages))
	}
}

func TestPagesExecuteAgainstTheirViews(t *testing.T) {
	pages, err := parsePages()
	if err != nil {
		t.Fatalf("parsePages: %v", err)
	}

	cases := map[string]any{
		"home": homeView{
			baseView: testBase(), Remaining: 240, Granted: 250,
			Queues:    []queueView{{ID: 1, Name: "Trottier"}},
			Recent:    []jobView{testJobView()},
			MaxCopies: 10, MaxSize: 1 << 20,
		},
		"jobs":   jobsView{baseView: testBase(), Jobs: []jobView{testJobView()}},
		"result": resultView{baseView: testBase(), Title: "Submitted", Message: "Job 1 sent"},
		"admin":  adminView{baseView: testBase()},
	}

	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := pages[name].ExecuteTemplate(&buf, "layout", data); err != nil {
				t.Fatalf("executing %q: %v", name, err)
			}
			if buf.Len() == 0 {
				t.Fatal("rendered nothing")
			}
			if !strings.Contains(buf.String(), "<html") {
				t.Error("output is missing the layout wrapper")
			}
		})
	}
}

func TestPagesExecuteWithEmptyData(t *testing.T) {
	pages, err := parsePages()
	if err != nil {
		t.Fatalf("parsePages: %v", err)
	}
	var buf bytes.Buffer
	if err := pages["home"].ExecuteTemplate(&buf, "layout", homeView{baseView: testBase()}); err != nil {
		t.Errorf("home with no queues or jobs: %v", err)
	}
	buf.Reset()
	if err := pages["jobs"].ExecuteTemplate(&buf, "layout", jobsView{baseView: testBase()}); err != nil {
		t.Errorf("jobs with no jobs: %v", err)
	}
}

func TestRenderedJobNameIsEscaped(t *testing.T) {
	pages, err := parsePages()
	if err != nil {
		t.Fatalf("parsePages: %v", err)
	}
	j := testJobView()
	j.DocumentName = `<script>alert(1)</script>`

	var buf bytes.Buffer
	if err := pages["jobs"].ExecuteTemplate(&buf, "layout",
		jobsView{baseView: testBase(), Jobs: []jobView{j}}); err != nil {
		t.Fatalf("executing jobs: %v", err)
	}
	if strings.Contains(buf.String(), "<script>alert(1)</script>") {
		t.Error("document name rendered unescaped")
	}
}
