package transport

import (
	"net"
	"syscall"

	"github.com/wlynxg/anet"
)

// BindLANDials makes dials to an address on a directly connected subnet leave
// through that subnet's interface. Android routes an app's sockets over its
// default network only, and that is mobile data as soon as the Wi-Fi signal is
// weak, while the Wi-Fi stays connected: the PC next to the phone is then
// dialed over the mobile network and never answers. anet lists the interfaces
// where net.Interfaces is refused (Android 11+).
//
// Best effort: without the right to bind (kernels before 5.7) a dial goes
// where the system sends it, as it would without this.
func BindLANDials() {
	setDialControl(func(_, address string, c syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil
		}
		name := lanInterface(net.ParseIP(host), systemSubnets())
		if name == "" {
			return nil
		}
		return c.Control(func(fd uintptr) {
			_ = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, name)
		})
	})
}

// systemSubnets is the subnets of the interfaces that are up, by name.
func systemSubnets() map[string][]*net.IPNet {
	out := map[string][]*net.IPNet{}
	ifaces, err := anet.Interfaces()
	if err != nil {
		return out
	}
	for i := range ifaces {
		if ifaces[i].Flags&net.FlagUp == 0 || ifaces[i].Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := anet.InterfaceAddrsByInterface(&ifaces[i])
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				out[ifaces[i].Name] = append(out[ifaces[i].Name], n)
			}
		}
	}
	return out
}
