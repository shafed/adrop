// Package clipboard wraps the Wayland clipboard tools wl-copy / wl-paste.
//
// The SPEC targets Wayland (KDE/Sway/Hyprland) and specifies wl-clipboard for
// the PC side.
package clipboard

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

// Paste reads the current clipboard for the given MIME type via wl-paste.
// For "text/plain" (or empty) it adds -n to suppress the trailing newline.
func Paste(ctx context.Context) ([]byte, error) {
	return Get(ctx, "text/plain")
}

// Get reads the clipboard for the given MIME type.
func Get(ctx context.Context, mime string) ([]byte, error) {
	if mime == "" {
		mime = "text/plain"
	}
	args := []string{"-t", mime}
	if mime == "text/plain" {
		args = append([]string{"-n"}, args...)
	}
	cmd := exec.CommandContext(ctx, "wl-paste", args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("wl-paste: %w: %s", err, errb.String())
	}
	return out.Bytes(), nil
}

// Copy writes data to the clipboard via wl-copy with the given MIME type.
//
// wl-copy forks a long-lived background process that keeps serving the
// selection (standard Wayland behaviour: the source client must stay alive to
// answer paste requests). The foreground process exits as soon as that child
// is detached, so cmd.Run only waits on the foreground process.
//
// Crucially, Stdout/Stderr must be real *os.File handles, NOT a bytes.Buffer.
// With a buffer, os/exec installs an OS pipe and a copier goroutine, and
// cmd.Wait blocks until every process holding the pipe's write end closes it —
// including the detached wl-copy daemon, which never does (until the clipboard
// is overwritten). That left Copy hanging forever, so the receiver never sent
// its ack and the sending peer stalled. Routing to os.Stderr (journald for the
// systemd user service) keeps diagnostics without the inherited-pipe deadlock.
func Copy(ctx context.Context, data []byte, mime string) error {
	if mime == "" {
		mime = "text/plain"
	}
	cmd := exec.CommandContext(ctx, "wl-copy", "-t", mime)
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wl-copy: %w", err)
	}
	return nil
}
