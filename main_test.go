package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hunchom/claude-code-gateway/internal/config"
	"github.com/hunchom/claude-code-gateway/internal/state"
)

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
