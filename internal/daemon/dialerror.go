package daemon

import (
	"errors"
	"fmt"
	"net"
	"syscall"

	"github.com/shafed/adrop/internal/config"
)

// dialFailure turns a failed dial into something the user can act on.
//
// The common failures are one line apart in the raw error — every one of them
// ends in "connect: …" — yet they call for opposite fixes: put the phone back
// on this Wi-Fi, open the app, or repair the relay. Worse, the interesting
// detail (whether a wake was even attempted, and how it went) only ever
// reached the journal, so `adrop send` reported "connection refused" while the
// actual problem was a relay that wasn't listening.
//
// seenOnLAN reports whether the peer answered the mDNS resolve that runs
// before the wake path; it is the difference between "asleep" and "not here".
func dialFailure(dev config.Device, err error, seenOnLAN bool, wake string) error {
	host := dev.Addr
	if h, _, splitErr := net.SplitHostPort(dev.Addr); splitErr == nil {
		host = h
	}

	var msg string
	switch {
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		msg = fmt.Sprintf("%s is not on this network: no route to %s. "+
			"Put both devices on the same Wi-Fi — a wake push only opens the phone's "+
			"receive window, it cannot create a route.", dev.Name, host)
	case errors.Is(err, syscall.ECONNREFUSED):
		if seenOnLAN {
			msg = fmt.Sprintf("%s is on this network but nothing is listening on %s: "+
				"the app's receive window is closed.", dev.Name, dev.Addr)
		} else {
			msg = fmt.Sprintf("nothing is listening on %s, and %s is not announcing "+
				"itself on this network either — it is probably on another Wi-Fi or on "+
				"mobile data, and %s is a stale address.", dev.Addr, dev.Name, host)
		}
	case isTimeout(err):
		msg = fmt.Sprintf("%s did not answer at %s in time: it may be asleep, or this "+
			"network may isolate clients from each other (guest Wi-Fi often does).",
			dev.Name, dev.Addr)
	default:
		msg = fmt.Sprintf("cannot reach %s at %s: %v", dev.Name, dev.Addr, err)
	}
	if wake != "" {
		msg += " " + wake
	}
	return errors.New(msg)
}

// isTimeout reports whether err is a network timeout (including a dial that
// exhausted its deadline).
func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
