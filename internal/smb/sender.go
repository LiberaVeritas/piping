package smb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"piping/internal/queue"
)

type Sender struct {
	// authFile for smbclient:
	// username =
	// password =
	// domain =
	authFile string
	log      *slog.Logger
}

func New(authFile string, log *slog.Logger) *Sender {
	return &Sender{authFile: authFile, log: log}
}

func (s *Sender) Send(ctx context.Context, dest queue.Destination, payload []byte) error {
	f, err := os.CreateTemp("", "piping-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	path := f.Name()
	defer os.Remove(path)
	_, err = f.Write(payload)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	err = f.Close()
	if err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	cmd := exec.CommandContext(ctx, "smbclient", dest.Address,
		"-A", s.authFile,
		"-m", "SMB3",
		"-c", "print "+path,
	)
	out, runErr := cmd.CombinedOutput()

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("smb send to %s: %w", dest.Address, context.DeadlineExceeded)
	}
	if runErr != nil {
		status := ntStatuses(out)
		if status != "" {
			return fmt.Errorf("smb send to %s: %w (%s)", dest.Address, runErr, status)
		}
		return fmt.Errorf("smb send to %s: %w: %s", dest.Address, runErr, truncate(out, 300))
	}
	return nil
}

var reNTStatus = regexp.MustCompile(`NT_STATUS_[A-Z_0-9]+`)

// extracts NT_STATUS codes from smbclient output, in order of appearance
func ntStatuses(out []byte) string {
	seen := map[string]bool{}
	var codes []string
	for _, m := range reNTStatus.FindAllString(string(out), -1) {
		if !seen[m] {
			seen[m] = true
			codes = append(codes, m)
		}
	}
	return strings.Join(codes, ", ")
}

func truncate(out []byte, n int) string {
	s := strings.TrimSpace(string(out))
	if len(s) <= n {
		return s
	}
	return s[:n] + "…(truncated)"
}
