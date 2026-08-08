package smb

import (
	"bytes"
	"context"
	"fmt"
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
}

func New(authFile string) *Sender {
	return &Sender{authFile: authFile}
}

func (s *Sender) Send(ctx context.Context, dest queue.Destination, payload []byte) error {
	cmd := exec.CommandContext(ctx, "smbclient", "//"+dest.Address, "-A", s.authFile, "-m", "SMB3", "-c", "print -")
	cmd.Stdin = bytes.NewReader(payload)
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("smb send to %q: %w", dest.Address, ctxErr)
		}
		if status := ntStatuses(out); status != "" {
			return fmt.Errorf("smb send to %q: %w (%q)", dest.Address, err, status)
		}
		return fmt.Errorf("smb send to %q: %w: %q", dest.Address, err, truncate(out, 300))
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
