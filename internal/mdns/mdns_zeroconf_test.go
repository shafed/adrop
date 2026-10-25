//go:build windows || zeroconf

package mdns

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestAdvertiseIsBrowsed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Advertise(ctx, "adrop-zeroconf-test", 54321, "feedface")
	time.Sleep(500 * time.Millisecond)

	var mu sync.Mutex
	got := map[string]string{}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ResolveOnce(ctx, func(name, addr, fp string) {
			mu.Lock()
			got[name] = addr + " " + fp
			mu.Unlock()
		})
		mu.Lock()
		v, ok := got["adrop-zeroconf-test"]
		mu.Unlock()
		if ok {
			t.Logf("browsed: %s", v)
			return
		}
	}
	t.Fatalf("own announcement never browsed; saw %v", got)
}
