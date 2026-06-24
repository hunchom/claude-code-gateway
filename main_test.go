package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hunchom/claude-code-gateway/internal/config"
	"github.com/hunchom/claude-code-gateway/internal/state"
)

func TestUpstreamCount(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages/count_tokens" {
			_, _ = w.Write([]byte(`{"input_tokens":11}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ok.Close()
	cfg := config.Default()
	cfg.Upstream = ok.URL
	tok, status, err := upstreamCount(cfg, nil, []byte(`{"model":"m","messages":[]}`))
	if err != nil || status != http.StatusOK || tok != 11 {
		t.Fatalf("upstreamCount = (%d, %d, %v), want (11, 200, nil)", tok, status, err)
	}

	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer missing.Close()
	cfg.Upstream = missing.URL
	if _, status, err := upstreamCount(cfg, nil, []byte(`{}`)); err == nil || status != http.StatusNotFound {
		t.Errorf("want 404 error, got status=%d err=%v", status, err)
	}
}

func TestBuiltinSamples(t *testing.T) {
	samples := builtinSamples("claude-test")
	if len(samples) == 0 {
		t.Fatal("no samples")
	}
	for _, s := range samples {
		var req map[string]any
		if err := json.Unmarshal(s.body, &req); err != nil {
			t.Fatalf("%s: invalid JSON: %v", s.name, err)
		}
		if req["model"] != "claude-test" {
			t.Errorf("%s: model = %v, want claude-test", s.name, req["model"])
		}
	}
}

func TestLiveProbe(t *testing.T) {
	supported := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages/count_tokens" {
			_, _ = w.Write([]byte(`{"input_tokens":3}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer supported.Close()
	cfg := config.Default()
	cfg.Upstream = supported.URL
	if c, _ := liveProbe(cfg, nil, "claude-x"); c != state.Supported {
		t.Errorf("supported probe = %v, want supported", c)
	}

	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer missing.Close()
	cfg.Upstream = missing.URL
	if c, _ := liveProbe(cfg, nil, "claude-x"); c != state.Unsupported {
		t.Errorf("missing probe = %v, want unsupported", c)
	}
}
