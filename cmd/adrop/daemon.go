package main

import (
	"context"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/shafed/adrop/internal/config"
	"github.com/shafed/adrop/internal/daemon"
)

// runDaemon starts the resident process and blocks until SIGINT/SIGTERM.
func runDaemon(args []string) error {
	store, err := config.Open("")
	if err != nil {
		return err
	}
	port := daemon.DefaultPort
	if p := os.Getenv("ADROP_PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}
	d, err := daemon.New(daemon.Options{
		Store:       store,
		Name:        os.Getenv("ADROP_NAME"),
		Port:        port,
		AdvertiseIP: os.Getenv("ADROP_ADVERTISE_IP"),
		DownloadDir: os.Getenv("ADROP_DOWNLOAD_DIR"),
		RelayAddr:   os.Getenv("ADROP_RELAY"),
		Logger:      log.New(logOutput(), "adrop: ", log.LstdFlags),
		TapPair:     os.Getenv("ADROP_TAP_PAIR") == "1",
		Reticulum:   os.Getenv("ADROP_RNS") == "1",
		RNSHubs:     splitList(os.Getenv("ADROP_RNS_HUBS")),
	})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := d.Run(ctx); err != nil && err != context.Canceled {
		return err
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// logOutput is stderr (journald under systemd); on Windows the daemon runs
// without a console, so it logs to daemon.log in its config directory.
func logOutput() io.Writer {
	if runtime.GOOS != "windows" {
		return os.Stderr
	}
	path := filepath.Join(config.Dir(), "daemon.log")
	os.MkdirAll(filepath.Dir(path), 0o700)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return os.Stderr
	}
	return f
}
