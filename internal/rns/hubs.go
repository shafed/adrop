package rns

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// BuiltinHubs are public Reticulum hubs that held a connection and streamed
// announces when measured from a Russian home network on 2026-10-25. The
// catalog adds more; bad ones are replaced either way.
var BuiltinHubs = []string{
	"rns.michmesh.net:7822",
	"rns.pawgslayers.club:4242",
	"rns.buzag.ru:4242",
	"ralsei.ru:4343",
	"suah.dev:4343",
	"rns.mari-el.net:4242",
	"reticulum.nepamesh.com:4242",
	"rns.utn.lol:4965",
	"reticulum.washmesh.net:7242",
	"rns.simplyequipped.com:4242",
	"rns.radical.computer:4242",
	"rns.noderage.org:4242",
	"rns.reticulum.world:6666",
	"rmap.world:4242",
}

// CatalogURL lists community hubs as JSON (directory.rns.recipes).
const CatalogURL = "https://directory.rns.recipes/api/directory/submitted"

// HubPolicy tunes HubManager. Zero fields take the defaults.
type HubPolicy struct {
	Want          int           // hubs kept connected (3)
	Check         time.Duration // how often hubs are measured (15 s)
	ConnectGrace  time.Duration // time a new hub has to come up (20 s)
	Silence       time.Duration // a hub that sends nothing this long is bad (90 s)
	Backoff       time.Duration // a bad hub is not retried for this long (30 min)
	AnnounceEvery time.Duration // re-announce period (10 min)
}

func (p HubPolicy) withDefaults() HubPolicy {
	def := func(v *time.Duration, d time.Duration) {
		if *v == 0 {
			*v = d
		}
	}
	if p.Want == 0 {
		p.Want = 3
	}
	def(&p.Check, 15*time.Second)
	def(&p.ConnectGrace, 20*time.Second)
	def(&p.Silence, 90*time.Second)
	def(&p.Backoff, 30*time.Minute)
	def(&p.AnnounceEvery, 10*time.Minute)
	return p
}

// HubManager keeps Policy.Want working hubs connected: a hub counts as
// working only while it holds the connection and keeps delivering data (every
// hub relays the network's announces, so silence means it is dead or
// filtered). Bad hubs are dropped and replaced from the candidates.
type HubManager struct {
	node   *Node
	policy HubPolicy
	logf   func(format string, args ...any)

	mu         sync.Mutex
	candidates []string
	state      map[string]*hubState
	bad        map[string]time.Time
}

type hubState struct {
	added     time.Time
	announced bool // announced through this hub since it came up
	lastRx    uint64
	lastData  time.Time
}

// NewHubManager manages node's hubs, taking candidates in order.
func NewHubManager(node *Node, candidates []string, policy HubPolicy, logf func(string, ...any)) *HubManager {
	return &HubManager{
		node:       node,
		policy:     policy.withDefaults(),
		logf:       logf,
		candidates: dedup(candidates),
		state:      make(map[string]*hubState),
		bad:        make(map[string]time.Time),
	}
}

// AddCandidates appends hubs (e.g. from the catalog) after the current ones.
func (m *HubManager) AddCandidates(hubs []string) {
	m.mu.Lock()
	m.candidates = dedup(append(m.candidates, hubs...))
	m.mu.Unlock()
}

// Run measures and replaces hubs until ctx is done.
func (m *HubManager) Run(ctx context.Context) {
	m.fill()
	tick := time.NewTicker(m.policy.Check)
	defer tick.Stop()
	announce := time.NewTicker(m.policy.AnnounceEvery)
	defer announce.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			m.measure()
			m.fill()
		case <-announce.C:
			m.node.Announce()
		}
	}
}

// Working lists the hubs that currently pass the measurement.
func (m *HubManager) Working() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for addr, st := range m.state {
		if !st.lastData.IsZero() {
			out = append(out, addr)
		}
	}
	return out
}

func (m *HubManager) measure() {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for addr, st := range m.state {
		connected, rx, _, ok := m.node.HubStats(addr)
		if !ok {
			delete(m.state, addr)
			continue
		}
		if connected && !st.announced {
			// Announce as soon as the hub is up, so it learns a path to us.
			st.announced = true
			go m.node.Announce()
		}
		if rx > st.lastRx {
			st.lastRx, st.lastData = rx, now
		}
		var reason string
		switch {
		case !connected && now.Sub(st.added) > m.policy.ConnectGrace:
			reason = "connection does not hold"
		case st.lastData.IsZero() && now.Sub(st.added) > m.policy.Silence:
			reason = "connected but sent nothing"
		case !st.lastData.IsZero() && now.Sub(st.lastData) > m.policy.Silence:
			reason = fmt.Sprintf("silent for %s", now.Sub(st.lastData).Round(time.Second))
		}
		if reason != "" {
			m.logf("rns: dropping hub %s: %s", addr, reason)
			delete(m.state, addr)
			m.bad[addr] = now
			go m.node.DropHub(addr)
		}
	}
}

func (m *HubManager) fill() {
	m.mu.Lock()
	need := m.policy.Want - len(m.state)
	var picks []string
	now := time.Now()
	// Candidates are taken in order, not at random: paired devices then sit
	// on the same hubs and reach each other through one of them, instead of
	// across the wider network, where paths are slow to spread.
	for _, addr := range m.candidates {
		if need <= 0 {
			break
		}
		if _, used := m.state[addr]; used {
			continue
		}
		if at, isBad := m.bad[addr]; isBad && now.Sub(at) < m.policy.Backoff {
			continue
		}
		m.state[addr] = &hubState{added: now}
		picks = append(picks, addr)
		need--
	}
	m.mu.Unlock()
	for _, addr := range picks {
		if err := m.node.ConnectHub(addr); err != nil {
			m.logf("rns: hub %s: %v", addr, err)
			m.mu.Lock()
			delete(m.state, addr)
			m.bad[addr] = now
			m.mu.Unlock()
			continue
		}
		m.logf("rns: connecting to hub %s", addr)
		go m.announceWhenUp(addr)
	}
}

// announceWhenUp announces through a new hub as soon as its connection is
// up, so the hub learns a path to this device without waiting for a check.
func (m *HubManager) announceWhenUp(addr string) {
	deadline := time.Now().Add(m.policy.ConnectGrace)
	for time.Now().Before(deadline) {
		if connected, _, _, ok := m.node.HubStats(addr); !ok {
			return
		} else if connected {
			m.mu.Lock()
			st := m.state[addr]
			first := st != nil && !st.announced
			if first {
				st.announced = true
			}
			m.mu.Unlock()
			if first {
				m.node.Announce()
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func dedup(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// FetchCatalog downloads the community hub list and keeps the reachable
// kinds: clearnet TCP and backbone entries marked online. The result is
// also written to cachePath; on a failed download the cached copy is used.
func FetchCatalog(ctx context.Context, url, cachePath string) ([]string, error) {
	raw, err := download(ctx, url)
	if err == nil {
		os.WriteFile(cachePath, raw, 0o600)
	} else if cached, cerr := os.ReadFile(cachePath); cerr == nil {
		raw, err = cached, nil
	}
	if err != nil {
		return nil, err
	}
	return parseCatalog(raw)
}

func download(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog: %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func parseCatalog(raw []byte) ([]string, error) {
	var doc struct {
		Data []struct {
			Type    string `json:"type"`
			Network string `json:"network"`
			Host    string `json:"host"`
			Port    int    `json:"port"`
			Status  string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	var out []string
	for _, e := range doc.Data {
		if e.Network != "clearnet" || e.Status != "online" || e.Host == "" || e.Port <= 0 {
			continue
		}
		if e.Type != "tcp" && e.Type != "backbone" {
			continue
		}
		out = append(out, net.JoinHostPort(e.Host, strconv.Itoa(e.Port)))
	}
	return out, nil
}
