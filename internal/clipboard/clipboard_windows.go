package clipboard

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// On Windows the clipboard goes through PowerShell's Get-Clipboard and
// Set-Clipboard, text only. The text is passed as UTF-8 on stdin and stdout.

const psPrelude = "[Console]::InputEncoding=[Console]::OutputEncoding=[Text.Encoding]::UTF8;"

func powershell(ctx context.Context, script string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", psPrelude+script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("powershell: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// Paste reads the clipboard text.
func Paste(ctx context.Context) ([]byte, error) { return Get(ctx, "text/plain") }

// Get reads the clipboard; only text is supported on Windows.
func Get(ctx context.Context, mime string) ([]byte, error) {
	if mime != "" && mime != "text/plain" {
		return nil, fmt.Errorf("clipboard: %s is not supported on Windows", mime)
	}
	out, err := powershell(ctx, "Get-Clipboard -Raw | Write-Output -NoEnumerate", nil)
	if err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(bytes.TrimSuffix(out, []byte("\n")), []byte("\r")), nil
}

// Copy puts text on the clipboard.
func Copy(ctx context.Context, data []byte, mime string) error {
	if mime != "" && mime != "text/plain" {
		return fmt.Errorf("clipboard: %s is not supported on Windows", mime)
	}
	_, err := powershell(ctx, "$t=[Console]::In.ReadToEnd(); Set-Clipboard -Value $t", data)
	return err
}
