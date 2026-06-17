// Package fcm sends Firebase Cloud Messaging data messages via the HTTP v1 API
// using a service-account JSON credential for OAuth2 authentication.
//
// No external dependencies: JWT is signed with crypto/rsa via the private key
// embedded in the service-account JSON, and token exchange uses net/http.
package fcm

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const fcmSendURL = "https://fcm.googleapis.com/v1/projects/%s/messages:send"
const tokenURL = "https://oauth2.googleapis.com/token"
const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// ServiceAccount holds the parsed fields from a Firebase Admin SDK JSON key file.
type ServiceAccount struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

// Client sends FCM messages for a given service account. It caches OAuth2
// tokens and refreshes them automatically.
type Client struct {
	sa      ServiceAccount
	privKey *rsa.PrivateKey

	mu      sync.Mutex
	token   string
	expires time.Time
}

// NewClientFromJSON parses a Firebase Admin SDK service-account JSON and
// returns a ready Client.
func NewClientFromJSON(jsonBytes []byte) (*Client, error) {
	var sa ServiceAccount
	if err := json.Unmarshal(jsonBytes, &sa); err != nil {
		return nil, fmt.Errorf("fcm: parse service account: %w", err)
	}
	if sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, fmt.Errorf("fcm: service account missing required fields")
	}
	key, err := parseRSAKey(sa.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("fcm: parse private key: %w", err)
	}
	return &Client{sa: sa, privKey: key}, nil
}

// Send sends a data-only FCM message to the given registration token.
// data is a map of string key-value pairs delivered to the app.
func (c *Client) Send(ctx context.Context, registrationToken string, data map[string]string) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}

	// android.priority=high asks FCM to deliver immediately and wake the app
	// even when it is in the background / Doze. Without it, data-only messages
	// can be deferred indefinitely or dropped when the app has been stopped —
	// which defeats the wake-on-send purpose, especially on aggressive OEMs.
	type androidConfig struct {
		Priority string `json:"priority"`
	}
	type fcmMessage struct {
		Token   string            `json:"token"`
		Data    map[string]string `json:"data"`
		Android androidConfig     `json:"android"`
	}
	type fcmRequest struct {
		Message fcmMessage `json:"message"`
	}
	body, err := json.Marshal(fcmRequest{Message: fcmMessage{
		Token:   registrationToken,
		Data:    data,
		Android: androidConfig{Priority: "high"},
	}})
	if err != nil {
		return err
	}

	url := fmt.Sprintf(fcmSendURL, c.sa.ProjectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fcm: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("fcm: send: status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// accessToken returns a valid OAuth2 bearer token, refreshing if needed.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.expires.Add(-30 * time.Second)) {
		return c.token, nil
	}

	jwt, err := c.signedJWT()
	if err != nil {
		return "", err
	}

	body := "grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Ajwt-bearer&assertion=" + jwt
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fcm: token fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("fcm: token fetch: status %d: %s", resp.StatusCode, string(b))
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("fcm: token decode: %w", err)
	}
	c.token = tok.AccessToken
	c.expires = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return c.token, nil
}

// signedJWT creates and signs a JWT assertion for the token endpoint.
func (c *Client) signedJWT() (string, error) {
	now := time.Now().Unix()
	header := b64url(mustJSON(map[string]string{"alg": "RS256", "typ": "JWT"}))
	claims := b64url(mustJSON(map[string]interface{}{
		"iss":   c.sa.ClientEmail,
		"scope": fcmScope,
		"aud":   tokenURL,
		"iat":   now,
		"exp":   now + 3600,
	}))
	sigInput := header + "." + claims
	digest := sha256.Sum256([]byte(sigInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.privKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("fcm: sign JWT: %w", err)
	}
	return sigInput + "." + b64url(sig), nil
}

func b64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

func parseRSAKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not an RSA key")
	}
	return rk, nil
}
