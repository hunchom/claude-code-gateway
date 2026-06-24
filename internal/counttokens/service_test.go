package counttokens

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

func TestDoUpstreamRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable) // transient
			return
		}
		_, _ = w.Write([]byte(`{"input_tokens":7}`))
	}))
	defer srv.Close()

	s := &Service{upstream: srv.URL, client: srv.Client()}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	resp, err := s.doUpstream(r, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d after retries, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("upstream calls = %d, want 3 (2 retries)", got)
	}
}

func TestDoUpstreamNoRetryOn501(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusNotImplemented) // definitive: unsupported
	}))
	defer srv.Close()

	s := &Service{upstream: srv.URL, client: srv.Client()}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	resp, err := s.doUpstream(r, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("upstream calls = %d, want 1 (501 must not retry)", got)
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
	if got.LocalTokenizer != "not-started" {
		t.Errorf("local_tokenizer = %q, want not-started for a fresh service", got.LocalTokenizer)
	}
}

func TestHeuristicCount(t *testing.T) {
	req := &sdkRequest{
		Messages:    []sdkMessage{{Role: "user", Content: []sdkPart{{Type: "text", Text: "aaaaaaaa"}}}},
		ExtraTokens: 5,
	}
	if got := heuristicCount(req); got < 5 {
		t.Errorf("heuristicCount = %d, want >= ExtraTokens (5)", got)
	}
	if got := heuristicCount(&sdkRequest{}); got != 1 {
		t.Errorf("empty request heuristicCount = %d, want floor of 1", got)
	}
}

func TestServeLocalHeuristicFallback(t *testing.T) {
	s := &Service{opts: Options{ImageTokens: 1600, PDFTokens: 3000}}
	// Force the tokenizer pool to be unavailable without touching Node.
	s.poolOnce.Do(func() { s.poolErr = errors.New("node unavailable") })

	rec := httptest.NewRecorder()
	s.serveLocal(rec, []byte(`{"model":"claude-x","messages":[{"role":"user","content":"hello world"}]}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (must not hard-fail)", rec.Code)
	}
	if got := rec.Header().Get("X-Ccgate-Count"); got != "heuristic" {
		t.Errorf("X-Ccgate-Count = %q, want heuristic", got)
	}
	var out map[string]int
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["input_tokens"] < 1 {
		t.Errorf("input_tokens = %d, want >= 1", out["input_tokens"])
	}
}
