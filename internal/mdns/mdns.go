// Package mdns advertises and discovers the _adrop._tcp DNS-SD service using
// the avahi CLI tools (avahi-publish-service and avahi-browse from avahi-utils).
//
// Security note: mDNS records are used only to update the IP/port of already-
// paired devices. The actual connection still uses pinned mTLS, so a spoofed
// mDNS record can cause a connection attempt to the wrong host, which will fail
// the TLS fingerprint check and be rejected.
package mdns

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Advertise runs avahi-publish-service in the background, announcing name on
// _adrop._tcp at port with a TXT record "fp=<fingerprint>".
// It restarts avahi-publish-service if it exits unexpectedly and blocks until
// ctx is cancelled. If avahi-publish-service is not installed it logs a warning
// and returns nil.
func Advertise(ctx context.Context, name string, port int, fingerprint string) error {
	if _, err := exec.LookPath("avahi-publish-service"); err != nil {
		log.Printf("mdns: avahi-publish-service not found, skipping advertisement (%v)", err)
		return nil
	}
	for {
		cmd := exec.CommandContext(ctx,
			"avahi-publish-service",
			name,
			"_adrop._tcp",
			strconv.Itoa(port),
			"fp="+fingerprint,
		)
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("mdns: avahi-publish-service exited: %v; restarting in 5s", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(5 * time.Second):
			}
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// Browse runs avahi-browse -rtp _adrop._tcp and calls onRecord for each
// resolved peer record with (name, "addr:port", fingerprint-from-TXT).
// It blocks until ctx is cancelled. If avahi-browse is not installed it logs a
// warning and returns nil.
func Browse(ctx context.Context, onRecord func(name, addr, fp string)) error {
	if _, err := exec.LookPath("avahi-browse"); err != nil {
		log.Printf("mdns: avahi-browse not found, skipping discovery (%v)", err)
		return nil
	}
	for {
		if err := runBrowse(ctx, onRecord); err != nil && ctx.Err() == nil {
			log.Printf("mdns: avahi-browse exited: %v; restarting in 5s", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(5 * time.Second):
			}
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

func runBrowse(ctx context.Context, onRecord func(name, addr, fp string)) error {
	cmd := exec.CommandContext(ctx, "avahi-browse", "-rtp", "_adrop._tcp")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if name, addr, fp, ok := parseBrowseLine(line); ok {
			onRecord(name, addr, fp)
		}
	}
	return cmd.Wait()
}

// parseBrowseLine parses a resolved record from avahi-browse -rtp output.
// Resolved lines start with '=' and have this format:
//
//	=;eth0;IPv4;name;_adrop._tcp;local;hostname.local;192.168.1.5;53127;txt="fp=abc123"
//
// Fields: event, iface, proto, name, type, domain, hostname, address, port, txt...
func parseBrowseLine(line string) (name, addr, fp string, ok bool) {
	if !strings.HasPrefix(line, "=") {
		return "", "", "", false
	}
	fields := strings.SplitN(line, ";", 10)
	if len(fields) < 10 {
		return "", "", "", false
	}
	// fields[3]=name, fields[7]=address, fields[8]=port, fields[9]=txt
	recName := fields[3]
	address := fields[7]
	portStr := fields[8]
	txtField := fields[9]

	if recName == "" || address == "" || portStr == "" {
		return "", "", "", false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", "", "", false
	}

	fp = extractFP(txtField)
	if fp == "" {
		return "", "", "", false
	}

	return recName, fmt.Sprintf("%s:%d", address, port), fp, true
}

// extractFP finds "fp=<value>" in a TXT record string.
// The field may look like: txt="fp=abc123" or fp=abc123 or "fp=abc123" "key=val"
func extractFP(txt string) string {
	// Split by whitespace and quotes to find the fp= token
	for _, part := range strings.Fields(txt) {
		part = strings.Trim(part, `"'`)
		if strings.HasPrefix(part, "fp=") {
			return strings.TrimPrefix(part, "fp=")
		}
	}
	return ""
}
