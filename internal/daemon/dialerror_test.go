package daemon

import (
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/shafed/adrop/internal/config"
)

// opErr wraps a syscall errno the way the net package does, so the test feeds
// dialFailure exactly what transport.Dial returns.
func opErr(errno syscall.Errno) error {
	return &net.OpError{
		Op:   "dial",
		Net:  "tcp",
		Addr: &net.TCPAddr{IP: net.IPv4(192, 168, 0, 119), Port: 7777},
		Err:  &os.SyscallError{Syscall: "connect", Err: errno},
	}
}

type fakeTimeout struct{}

func (fakeTimeout) Error() string   { return "i/o timeout" }
func (fakeTimeout) Timeout() bool   { return true }
func (fakeTimeout) Temporary() bool { return true }

func TestDialFailureExplainsTheCause(t *testing.T) {
	dev := config.Device{Name: "SM-S721B", Addr: "192.168.0.119:7777"}

	cases := []struct {
		name      string
		err       error
		seenOnLAN bool
		wake      string
		want      []string
		reject    []string
	}{
		{
			name: "no route means the phone is on another network",
			err:  opErr(syscall.EHOSTUNREACH),
			want: []string{"not on this network", "192.168.0.119", "same Wi-Fi"},
		},
		{
			name: "network unreachable reads the same way",
			err:  opErr(syscall.ENETUNREACH),
			want: []string{"not on this network"},
		},
		{
			name:      "refused while announcing itself means the app is closed",
			err:       opErr(syscall.ECONNREFUSED),
			seenOnLAN: true,
			want:      []string{"is on this network", "receive window is closed"},
			reject:    []string{"not on this network"},
		},
		{
			name:   "refused with no mDNS answer means a stale address",
			err:    opErr(syscall.ECONNREFUSED),
			want:   []string{"not announcing", "stale address"},
			reject: []string{"receive window is closed"},
		},
		{
			name: "timeout suggests sleep or client isolation",
			err:  &net.OpError{Op: "dial", Net: "tcp", Err: fakeTimeout{}},
			want: []string{"did not answer", "isolate clients"},
		},
		{
			name: "the wake outcome is appended, not hidden in the journal",
			err:  opErr(syscall.ECONNREFUSED),
			wake: "The wake relay at http://localhost:18080 could not be reached: connection refused.",
			want: []string{"wake relay at http://localhost:18080"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dialFailure(dev, tc.err, tc.seenOnLAN, tc.wake).Error()
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("message %q does not mention %q", got, want)
				}
			}
			for _, reject := range tc.reject {
				if strings.Contains(got, reject) {
					t.Errorf("message %q should not mention %q", got, reject)
				}
			}
			if !strings.Contains(got, dev.Name) {
				t.Errorf("message %q never names the device", got)
			}
		})
	}
}

// TestDialFailureKeepsUnknownErrors checks that an error we have no advice for
// still carries the original text rather than being swallowed by a generic
// message.
func TestDialFailureKeepsUnknownErrors(t *testing.T) {
	dev := config.Device{Name: "phone", Addr: "10.0.0.5:7777"}
	got := dialFailure(dev, &net.OpError{Op: "dial", Err: syscall.EACCES}, false, "").Error()
	if !strings.Contains(got, "permission denied") {
		t.Errorf("message %q dropped the underlying error", got)
	}
}

func TestIsTimeout(t *testing.T) {
	if !isTimeout(&net.OpError{Op: "dial", Err: fakeTimeout{}}) {
		t.Error("a wrapped net timeout was not recognised")
	}
	if isTimeout(opErr(syscall.ECONNREFUSED)) {
		t.Error("connection refused was reported as a timeout")
	}
	if isTimeout(net.UnknownNetworkError("boom")) {
		t.Error("an unknown network error was reported as a timeout")
	}
}
