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

func TestClassify(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   state.Capability
	}{
		{200, `{"input_tokens":5}`, state.Supported},
		{404, `not found`, state.Unsupported},
		{405, ``, state.Unsupported},
		{501, ``, state.Unsupported},
		{200, `{"error":{"type":"not_found_error"}}`, state.Unsupported},
		{200, `{"foo":1}`, state.Unknown},
		{401, `auth required`, state.Unknown},
		{503, `upstream down`, state.Unknown},
	}
	for _, c := range cases {
		if got := Classify(c.status, []byte(c.body)); got != c.want {
			t.Errorf("Classify(%d, %q) = %v, want %v", c.status, c.body, got, c.want)
		}
	}
}

func TestStatus(t *testing.T) {
	s := &Service{opts: Options{Mode: config.CountAuto}, upstream: "https://up"}
	s.st = state.State{Endpoint: "https://up", CountTokens: state.Unsupported, CheckedAt: time.Now()}
	got := s.Status()
	if got.Mode != config.CountAuto || got.Upstream != "https://up" || got.CountTokensUpstream != "unsupported" {
		t.Errorf("status = %+v", got)
	}
	if got.CheckedAt == "" {
		t.Error("expected checked_at to be set")
	}
}
