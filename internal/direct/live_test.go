package direct

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pion/ice/v4"
)

// TestLiveSTUN gathers candidates with the public STUN servers and reports
// the outside address: ADROP_LIVE_STUN=1 go test ./internal/direct -run LiveSTUN -v
func TestLiveSTUN(t *testing.T) {
	if os.Getenv("ADROP_LIVE_STUN") == "" {
		t.Skip("set ADROP_LIVE_STUN=1 to query public STUN servers")
	}
	agent, err := newAgent(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	done := make(chan struct{})
	var srflx []string
	agent.OnCandidate(func(c ice.Candidate) {
		if c == nil {
			close(done)
			return
		}
		if c.Type() == ice.CandidateTypeServerReflexive {
			srflx = append(srflx, c.Address())
		}
	})
	if err := agent.GatherCandidates(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
	}
	if len(srflx) == 0 {
		t.Fatal("no server-reflexive candidate: STUN unreachable")
	}
	t.Logf("outside address seen by STUN: %s", strings.Join(srflx, ", "))
}
