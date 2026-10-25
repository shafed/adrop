//go:build windows || zeroconf

package mdns

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
)

// Where there is no avahi (Windows), the same DNS-SD service is announced
// and browsed in pure Go. Linux keeps avahi, which already owns port 5353
// there; `-tags zeroconf` builds this one on Linux for its test.

const (
	service = "_adrop._tcp"
	domain  = "local."
	// browseWindow restarts the browse now and then: zeroconf reports each
	// instance once per browse, and a new browse picks up changed addresses.
	browseWindow = time.Minute
)

// Advertise announces name on _adrop._tcp at port with TXT "fp=<fingerprint>"
// until ctx is cancelled.
func Advertise(ctx context.Context, name string, port int, fingerprint string) error {
	srv, err := zeroconf.Register(name, service, domain, port, []string{"fp=" + fingerprint}, nil)
	if err != nil {
		return fmt.Errorf("mdns: register: %w", err)
	}
	<-ctx.Done()
	srv.Shutdown()
	return nil
}

// Browse reports _adrop._tcp records with (name, "addr:port", fingerprint)
// until ctx is cancelled; onRemove (if non-nil) gets names whose records
// expired (TTL 0).
func Browse(ctx context.Context, onRecord func(name, addr, fp string), onRemove func(name string)) error {
	for ctx.Err() == nil {
		wctx, cancel := context.WithTimeout(ctx, browseWindow)
		err := browse(wctx, onRecord, onRemove)
		cancel()
		if err != nil {
			log.Printf("mdns: browse: %v", err)
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
		}
	}
	return nil
}

// ResolveOnce runs one short browse and reports what answered.
func ResolveOnce(ctx context.Context, onRecord func(name, addr, fp string)) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return browse(ctx, onRecord, nil)
}

func browse(ctx context.Context, onRecord func(name, addr, fp string), onRemove func(name string)) error {
	r, err := zeroconf.NewResolver(nil)
	if err != nil {
		return err
	}
	entries := make(chan *zeroconf.ServiceEntry, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range entries {
			if e.TTL == 0 {
				if onRemove != nil {
					onRemove(e.Instance)
				}
				continue
			}
			fp := ""
			for _, t := range e.Text {
				if v, ok := strings.CutPrefix(t, "fp="); ok {
					fp = v
				}
			}
			if fp == "" || len(e.AddrIPv4) == 0 {
				continue
			}
			onRecord(e.Instance, net.JoinHostPort(e.AddrIPv4[0].String(), fmt.Sprint(e.Port)), fp)
		}
	}()
	if err := r.Browse(ctx, service, domain, entries); err != nil {
		return err
	}
	<-ctx.Done()
	<-done
	return nil
}
