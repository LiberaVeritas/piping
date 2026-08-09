package printticket

import (
	"bytes"
	"strings"
	"testing"

	"piping/internal/job"
)

func TestFromJobMapsPrintOptions(t *testing.T) {
	cases := []struct {
		name          string
		color, duplex bool
		wantMode      string
		wantSides     string
	}{
		{"mono simplex", false, false, ColorModeMonochrome, SidesOneSided},
		{"mono duplex", false, true, ColorModeMonochrome, SidesTwoSidedLongEdge},
		{"colour simplex", true, false, ColorModeColor, SidesOneSided},
		{"colour duplex", true, true, ColorModeColor, SidesTwoSidedLongEdge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := FromJob(job.Job{
				ID: 7, Username: "user", Copies: 3,
				Color: tc.color, Duplex: tc.duplex,
			})
			if a.ColorMode != tc.wantMode {
				t.Errorf("ColorMode = %q, want %q", a.ColorMode, tc.wantMode)
			}
			if a.Sides != tc.wantSides {
				t.Errorf("Sides = %q, want %q", a.Sides, tc.wantSides)
			}
			if a.Copies != 3 || a.User != "user" || a.JobName != "piping-job-7" {
				t.Errorf("attributes = %+v", a)
			}
		})
	}
}

func TestXCPTFramesTheDocument(t *testing.T) {
	doc := []byte("%PDF-1.4\nbody bytes\n")
	attrs := FromJob(job.Job{ID: 1, Username: "user", Copies: 1})
	out := XCPT(attrs, doc)

	if !bytes.HasPrefix(out, []byte(uel+"@PJL JOB\r\n")) {
		t.Error("ticket does not open with the UEL and @PJL JOB")
	}
	if !bytes.HasSuffix(out, []byte(uel+"@PJL EOJ\r\n"+uel)) {
		t.Error("ticket does not close with @PJL EOJ and a trailing UEL")
	}

	// the PDL must follow the language switch verbatim: any rewriting here
	// reaches the printer as a corrupt document.
	const enter = "@PJL ENTER LANGUAGE=PDF\r\n"
	if n := bytes.Count(out, []byte(enter)); n != 1 {
		t.Fatalf("found %d language switches, want exactly 1", n)
	}
	body := out[bytes.Index(out, []byte(enter))+len(enter):]
	if !bytes.HasPrefix(body, doc) {
		t.Error("document bytes do not follow the language switch unmodified")
	}

	for _, line := range xpifLines(attrs) {
		if !bytes.Contains(out, []byte("@PJL XCPT "+line+"\r\n")) {
			t.Errorf("xpif line not carried as a PJL XCPT line: %q", line)
		}
	}
}

// the username reaches the ticket from the session, so it must not be able to
// close a tag and inject attributes of its own.
func TestTicketEscapesAttributeValues(t *testing.T) {
	hostile := `x</job-originating-user-name><copies syntax="integer">999</copies>`
	out := string(XPIF(Attributes{Copies: 1, User: hostile, JobName: "piping-job-1"}, nil))

	if strings.Contains(out, hostile) {
		t.Error("attribute value embedded unescaped — the ticket can be broken out of")
	}
	if !strings.Contains(out, "&lt;/job-originating-user-name&gt;") {
		t.Error("angle brackets in an attribute value were not escaped")
	}
	if n := strings.Count(out, "<copies syntax=\"integer\">"); n != 1 {
		t.Errorf("found %d copies elements, want 1 — an injected one changed the job", n)
	}
}
