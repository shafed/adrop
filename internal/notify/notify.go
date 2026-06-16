// Package notify sends desktop notifications via notify-send (libnotify).
//
// Failures are non-fatal: a missing notify-send must never break a transfer,
// so Send logs nothing and simply returns the error for optional inspection.
package notify

import (
	"context"
	"os/exec"
)

// Send shows a desktop notification. appName groups notifications; it is
// passed via --app-name when supported.
func Send(ctx context.Context, summary, body string) error {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "notify-send",
		"--app-name=adrop",
		"--icon=phone",
		summary, body)
	return cmd.Run()
}
