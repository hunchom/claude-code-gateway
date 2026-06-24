package counttokens

import (
	"testing"
	"time"

	"github.com/hunchom/claude-code-gateway/internal/config"
	"github.com/hunchom/claude-code-gateway/internal/state"
)

func TestDecide(t *testing.T) {
	now := time.Now()
	s := &Service{opts: Options{Mode: config.CountAuto, RecheckHours: 6}, upstream: "https://up"}

	cases := []struct {
		name   string
		st     state.State
		forced bool
		want   decision
	}{
		{"unknown", state.State{Endpoint: "https://up", CountTokens: state.Unknown, CheckedAt: now}, false, decideProbe},
		{"fresh supported", state.State{Endpoint: "https://up", CountTokens: state.Supported, CheckedAt: now}, false, decidePassthrough},
		{"fresh unsupported", state.State{Endpoint: "https://up", CountTokens: state.Unsupported, CheckedAt: now}, false, decideLocal},
		{"stale", state.State{Endpoint: "https://up", CountTokens: state.Supported, CheckedAt: now.Add(-7 * time.Hour)}, false, decideProbe},
		{"forced", state.State{Endpoint: "https://up", CountTokens: state.Supported, CheckedAt: now}, true, decideProbe},
		{"endpoint changed", state.State{Endpoint: "https://other", CountTokens: state.Supported, CheckedAt: now}, false, decideProbe},
	}
	for _, c := range cases {
		s.st = c.st
		s.forced = c.forced
		if got := s.decide(); got != c.want {
			t.Errorf("%s: decide() = %v, want %v", c.name, got, c.want)
		}
	}

	// Explicit modes override the learned state.
	s.opts.Mode = config.CountLocal
	if got := s.decide(); got != decideLocal {
		t.Errorf("mode=local: decide() = %v, want decideLocal", got)
	}
	s.opts.Mode = config.CountPassthrough
	if got := s.decide(); got != decidePassthrough {
		t.Errorf("mode=passthrough: decide() = %v, want decidePassthrough", got)
	}
}
