package ghostscript

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strconv"

	"piping/internal/pdf"
)

type Analyzer struct {
	gsPath    string
	threshold float64 // inkcov chroma spread above which a page is color
	log       *slog.Logger
}

func New(colorSpreadThreshold float64, log *slog.Logger) *Analyzer {
	return &Analyzer{gsPath: "gs", threshold: colorSpreadThreshold, log: log}
}

func (a *Analyzer) VerifyDevice(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, a.gsPath, "-dNOPAUSE", "-dBATCH", "-dSAFER", "-dQUIET", "-q", "-sDEVICE=inkcov")
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return fmt.Errorf("running gs to verify inkcov device: %w", err)
	}
	if bytes.Contains(out, []byte("Unknown device")) {
		return errors.New("gs has no inkcov device")
	}
	return nil
}

// (Ghostscript bug 699342), sometimes no newlines
var cmykLine = regexp.MustCompile(`(\d+\.\d+)\s+(\d+\.\d+)\s+(\d+\.\d+)\s+(\d+\.\d+)\s+CMYK OK`)

func (a *Analyzer) CountPages(ctx context.Context, doc []byte) (pages, colorPages int, err error) {
	f, err := os.CreateTemp("", "piping-*.pdf")
	if err != nil {
		return 0, 0, fmt.Errorf("creating temp file: %w", err)
	}
	path := f.Name()
	defer os.Remove(path)
	_, err = f.Write(doc)
	if err != nil {
		_ = f.Close()
		return 0, 0, fmt.Errorf("writing temp file: %w", err)
	}
	err = f.Close()
	if err != nil {
		return 0, 0, fmt.Errorf("closing temp file: %w", err)
	}

	cmd := exec.CommandContext(ctx, a.gsPath, "-sOutputFile=%stdout%", "-dBATCH", "-dNOPAUSE", "-dQUIET", "-q", "-dSAFER", "-sDEVICE=inkcov", path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if runErr := cmd.Run(); runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return 0, 0, fmt.Errorf("gs rejected document (%s): %w", stderr.String(), pdf.ErrUnreadable)
		}
		return 0, 0, fmt.Errorf("running gs: %w", runErr)
	}

	pages, colorPages, perr := parseInkcov(stdout.String(), a.threshold)
	if perr != nil {
		return 0, 0, fmt.Errorf("parsing inkcov output: %w", perr)
	}
	if pages == 0 {
		return 0, 0, fmt.Errorf("gs reported no pages: %w", pdf.ErrUnreadable)
	}
	return pages, colorPages, nil
}

// a page is color iff the chroma channels' spread exceeds threshold.
func parseInkcov(out string, threshold float64) (pages, colorPages int, err error) {
	matches := cmykLine.FindAllStringSubmatch(out, -1)
	for _, m := range matches {
		cyan, e1 := strconv.ParseFloat(m[1], 64)
		magenta, e2 := strconv.ParseFloat(m[2], 64)
		yellow, e3 := strconv.ParseFloat(m[3], 64)
		if e1 != nil || e2 != nil || e3 != nil {
			return 0, 0, fmt.Errorf("bad coverage value in %q", m[0])
		}
		pages++
		if chromaSpread(cyan, magenta, yellow) > threshold {
			colorPages++
		}
	}
	return pages, colorPages, nil
}

func chromaSpread(c, m, y float64) float64 {
	return max(c, m, y) - min(c, m, y)
}
