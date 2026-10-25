//go:build windows || ipctcp

package ipc

import (
	"bufio"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestTokenEndpointAdmitsOnlyTheTokenHolder(t *testing.T) {
	t.Setenv("ADROP_IPC_FILE", filepath.Join(t.TempDir(), "ipc.json"))
	ln, addr, closeLn, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer closeLn()
	got := make(chan string, 2)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadString('\n')
			got <- line
			c.Close()
		}
	}()

	// Without the token: closed, never handed to the daemon.
	stranger, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	stranger.Write([]byte("not-the-token\n{\"cmd\":\"status\"}\n"))
	stranger.Close()

	c, err := Dial()
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte("{\"cmd\":\"status\"}\n"))
	select {
	case line := <-got:
		if line != "{\"cmd\":\"status\"}\n" {
			t.Fatalf("daemon read %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the token holder was not admitted")
	}
	select {
	case line := <-got:
		t.Fatalf("a connection without the token got through: %q", line)
	case <-time.After(300 * time.Millisecond):
	}
}
