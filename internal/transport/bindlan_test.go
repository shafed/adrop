package transport

import (
	"net"
	"testing"
)

func TestLANInterface(t *testing.T) {
	cidr := func(s string) *net.IPNet {
		ip, n, err := net.ParseCIDR(s)
		if err != nil {
			t.Fatal(err)
		}
		n.IP = ip
		return n
	}
	subnets := map[string][]*net.IPNet{
		"wlan0":  {cidr("192.168.0.132/24"), cidr("fe80::1/64")},
		"tun0":   {cidr("172.19.0.1/30")},
		"rmnet0": {cidr("10.208.141.161/24")},
	}
	cases := []struct {
		ip, want string
	}{
		{"192.168.0.123", "wlan0"},
		{"192.168.1.5", ""}, // private, on no connected subnet
		{"8.8.8.8", ""},     // public
		{"fe80::2", ""},     // link-local is not IsPrivate; it needs a zone anyway
	}
	for _, c := range cases {
		if got := lanInterface(net.ParseIP(c.ip), subnets); got != c.want {
			t.Errorf("lanInterface(%s) = %q, want %q", c.ip, got, c.want)
		}
	}
	if got := lanInterface(nil, subnets); got != "" {
		t.Errorf("lanInterface(nil) = %q", got)
	}
}
