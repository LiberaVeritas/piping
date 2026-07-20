package printticket

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"

	"piping/internal/job"
)

const (
	SidesOneSided         = "one-sided"
	SidesTwoSidedLongEdge = "two-sided-long-edge"
	ColorModeColor        = "color"
	ColorModeMonochrome   = "monochrome-grayscale"
)

const userDomain = "CAMPUS"

type Attributes struct {
	Copies    int
	Sides     string
	ColorMode string
	JobName   string
	User      string
}

func FromJob(j job.Job) Attributes {
	sides := SidesOneSided
	if j.Duplex {
		sides = SidesTwoSidedLongEdge
	}
	mode := ColorModeMonochrome
	if j.Color {
		mode = ColorModeColor
	}
	return Attributes{
		Copies:    j.Copies,
		Sides:     sides,
		ColorMode: mode,
		JobName:   fmt.Sprintf("piping-job-%d", j.ID),
		User:      j.Username,
	}
}

const LetterX = "21590"
const LetterY = "27940"

// ref: ticket format captured from output of Xerox driver
// which converts to postscript, but this works for pdf too
// XPIF (Xerox Printing Instruction Format) is xml based
// XCPT embeds XPIF within PJL (Print Job Language)
// PDL (Page Description Language) can be PostScript, PCL, PDF etc. in this case LANGUAGE=PDF
//
//	ESC%-12345X @PJL JOB
//	@PJL XCPT <?xml ...?>
//	@PJL XCPT <!DOCTYPE xpif SYSTEM "xpif-v02081.dtd">
//	@PJL XCPT ... (tab-indented) ...
//	@PJL XCPT </xpif>
//	@PJL ENTER LANGUAGE=PDF
//	<PDL bytes>
//	ESC%-12345X @PJL EOJ
//	ESC%-12345X
func xpifLines(a Attributes) []string {
	return []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<!DOCTYPE xpif SYSTEM "xpif-v02081.dtd">`,
		`<xpif version="1.0" cpss-version="2.07" xml:lang="en-US">`,
		"\t<job-template-attributes>",
		fmt.Sprintf("\t\t<color-effects-type syntax=\"keyword\">%s</color-effects-type>", xmlEscape(a.ColorMode)),
		fmt.Sprintf("\t\t<copies syntax=\"integer\">%d</copies>", a.Copies),
		"\t\t<job-sheets syntax=\"keyword\">none</job-sheets>",
		"\t\t<media-col syntax=\"collection\">",
		"\t\t\t<media-size syntax=\"collection\">",
		fmt.Sprintf("\t\t\t\t<x-dimension syntax=\"integer\">%s</x-dimension>", xmlEscape(LetterX)),
		fmt.Sprintf("\t\t\t\t<y-dimension syntax=\"integer\">%s</y-dimension>", xmlEscape(LetterY)),
		"\t\t\t</media-size>",
		"\t\t</media-col>",
		"\t\t<sheet-collate syntax=\"keyword\">collated</sheet-collate>",
		fmt.Sprintf("\t\t<sides syntax=\"keyword\">%s</sides>", xmlEscape(a.Sides)),
		"\t</job-template-attributes>",
		"\t<xpif-operation-attributes>",
		"\t\t<document-format syntax=\"mimeMediaType\">application/pdf</document-format>",
		fmt.Sprintf("\t\t<job-name syntax=\"name\" xml:space=\"preserve\">%s</job-name>", xmlEscape(a.JobName)),
		fmt.Sprintf("\t\t<job-originating-user-domain syntax=\"name\" xml:space=\"preserve\">%s</job-originating-user-domain>", xmlEscape(userDomain)),
		fmt.Sprintf("\t\t<job-originating-user-name syntax=\"name\" xml:space=\"preserve\">%s</job-originating-user-name>", xmlEscape(a.User)),
		fmt.Sprintf("\t\t<requesting-user-name syntax=\"name\" xml:space=\"preserve\">%s</requesting-user-name>", xmlEscape(a.User)),
		"\t</xpif-operation-attributes>",
		"</xpif>",
	}
}

func XPIF(a Attributes, doc []byte) []byte {
	var b bytes.Buffer
	b.WriteString(strings.Join(xpifLines(a), "\n"))
	b.WriteString("\n")
	b.Write(doc)
	return b.Bytes()
}

const uel = "\x1b%-12345X" // PJL Universal Exit Language

func XCPT(a Attributes, doc []byte) []byte {
	var b bytes.Buffer
	b.WriteString(uel)
	b.WriteString("@PJL JOB\r\n")
	for _, line := range xpifLines(a) {
		b.WriteString("@PJL XCPT ")
		b.WriteString(line)
		b.WriteString("\r\n")
	}
	b.WriteString("@PJL ENTER LANGUAGE=PDF\r\n")
	b.Write(doc)
	b.WriteString(uel)
	b.WriteString("@PJL EOJ\r\n")
	b.WriteString(uel)
	return b.Bytes()
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
