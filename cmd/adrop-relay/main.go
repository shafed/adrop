// adrop-relay is a minimal HTTP server that bridges a PC's "wake" request to
// a Firebase Cloud Messaging push targeted at a specific Android device.
//
// The relay does NOT see file contents — it only forwards a small JSON payload
// (fingerprint + sender name) so the phone can open its receive window. All
// actual data still flows directly over mTLS between PC and phone.
//
// Usage:
//
//	adrop-relay -key /path/to/serviceaccount.json [-addr :8080]
//
// POST /wake
//
//	{"fingerprint":"<hex>","sender":"<name>","fcm_token":"<token>"}
//
// Response: 200 OK on success, 4xx/5xx on failure.
//
// The relay stores a mapping of fingerprint → FCM token populated by the
// phone registering itself via POST /register (called by the daemon on
// the PC after it learns the token from a Hello exchange).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/shafed/adrop/internal/fcm"
)

var (
	addr    = flag.String("addr", ":8080", "listen address")
	keyFile = flag.String("key", "", "path to Firebase service account JSON (required)")
)

func main() {
	flag.Parse()
	if *keyFile == "" {
		fmt.Fprintln(os.Stderr, "adrop-relay: -key is required")
		os.Exit(1)
	}
	keyJSON, err := os.ReadFile(*keyFile)
	if err != nil {
		log.Fatalf("read key file: %v", err)
	}
	client, err := fcm.NewClientFromJSON(keyJSON)
	if err != nil {
		log.Fatalf("init FCM client: %v", err)
	}

	srv := &relay{fcm: client}
	mux := http.NewServeMux()
	mux.HandleFunc("/wake", srv.handleWake)

	log.Printf("adrop-relay listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

type relay struct {
	fcm *fcm.Client

	mu     sync.RWMutex
	tokens map[string]string // fingerprint → FCM registration token
}

type wakeRequest struct {
	Fingerprint string `json:"fingerprint"`
	Sender      string `json:"sender"`
	FCMToken    string `json:"fcm_token"`
}

func (r *relay) handleWake(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body wakeRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.FCMToken == "" {
		http.Error(w, "fcm_token is required", http.StatusBadRequest)
		return
	}

	ctx := context.Background()
	err := r.fcm.Send(ctx, body.FCMToken, map[string]string{
		"type":        "wake",
		"sender":      body.Sender,
		"fingerprint": body.Fingerprint,
	})
	if err != nil {
		log.Printf("FCM send error for %s: %v", body.Fingerprint[:min(16, len(body.Fingerprint))], err)
		http.Error(w, "fcm send failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	log.Printf("woke %s (sender=%s)", body.Fingerprint[:min(16, len(body.Fingerprint))], body.Sender)
	w.WriteHeader(http.StatusOK)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
