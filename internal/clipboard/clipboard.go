// Package clipboard wraps the Wayland clipboard tools wl-copy / wl-paste.
//
// The SPEC targets Wayland (KDE/Sway/Hyprland) and specifies wl-clipboard for
// the PC side. Only plain text is handled in the MVP.
package clipboard

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// Paste reads the current clipboard text via wl-paste.
func Paste(ctx context.Context) ([]byte, error) {
	// -n: don't append a trailing newline; -t text/plain: force text.
	cmd := exec.CommandContext(ctx, "wl-paste", "-n", "-t", "text/plain")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("wl-paste: %w: %s", err, errb.String())
	}
	return out.Bytes(), nil
}

// Copy writes data to the clipboard via wl-copy.
func Copy(ctx context.Context, data []byte, mime string) error {
	if mime == "" {
		mime = "text/plain"
	}
	cmd := exec.CommandContext(ctx, "wl-copy", "-t", mime)
	cmd.Stdin = bytes.NewReader(data)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wl-copy: %w: %s", err, errb.String())
	}
	return nil
}
