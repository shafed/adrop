package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
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
		Logger:      log.New(os.Stderr, "adrop: ", log.LstdFlags),
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
