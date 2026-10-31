//go:build !linux

package transport

// BindLANDials does nothing here: the phone, the only caller, runs Linux.
func BindLANDials() {}
