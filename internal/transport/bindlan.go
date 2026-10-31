package transport

import "net"

// lanInterface is the name of the interface whose subnet holds ip, "" if none
// or ip is not a private address. A public address is left to the system: the
// default route is the right way there, and a subnet a VPN claims with /0 is
// no local network.
func lanInterface(ip net.IP, subnets map[string][]*net.IPNet) string {
	if ip == nil || !ip.IsPrivate() {
		return ""
	}
	for name, nets := range subnets {
		for _, n := range nets {
			if n.Contains(ip) {
				return name
			}
		}
	}
	return ""
}
