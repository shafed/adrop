package notify

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
)

// Send shows a Windows toast through PowerShell and the WinRT notification
// API, under PowerShell's own app id (a registered one needs an installer).
func Send(ctx context.Context, summary, body string) error {
	esc := func(s string) string {
		r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "'", "''")
		return r.Replace(s)
	}
	script := `[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null;` +
		`[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] > $null;` +
		`$x = New-Object Windows.Data.Xml.Dom.XmlDocument;` +
		`$x.LoadXml('<toast><visual><binding template="ToastGeneric"><text>` + esc(summary) + `</text><text>` + esc(body) + `</text></binding></visual></toast>');` +
		`$id = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe';` +
		`[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($id).Show([Windows.UI.Notifications.ToastNotification]::new($x))`
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}
