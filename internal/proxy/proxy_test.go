package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hunchom/claude-code-gateway/internal/config"
)

func newTestGateway(t *testing.T, upstream string) *Gateway {
	t.Helper()
	cfg := config.Default()
	cfg.Upstream = upstream
	cfg.CountTokens = config.CountAuto
	cfg.StateDir = t.TempDir()
	gw, err := New(cfg, nil) // nil TLS config is fine for an http upstream
	if err != nil {
		t.Fatal(err)
	}
	return gw
}

func TestGatewayHealthAndStatus(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer up.Close()
	srv := httptest.NewServer(newTestGateway(t, up.URL))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), `"status":"ok"`) {
		t.Errorf("healthz = %d %s", resp.StatusCode, b)
	}

	resp2, err := http.Get(srv.URL + "/_ccgate/status")
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if !strings.Contains(string(b2), `"mode":"auto"`) || !strings.Contains(string(b2), up.URL) {
		t.Errorf("status = %s", b2)
	}
}

func TestGatewayPassthrough(t *testing.T) {
	var gotPath, gotMethod, gotBody, gotHeader, gotXFF, gotVia string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		bb, _ := io.ReadAll(r.Body)
		gotBody = string(bb)
		gotHeader = r.Header.Get("X-Test")
		gotXFF = r.Header.Get("X-Forwarded-For")
		gotVia = r.Header.Get("Via")
		w.Header().Set("X-Up", "1")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte("upstream-body"))
	}))
	defer up.Close()
	srv := httptest.NewServer(newTestGateway(t, up.URL))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/messages?beta=1", strings.NewReader(`{"hi":1}`))
	req.Header.Set("X-Test", "abc")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if gotPath != "/v1/messages" || gotMethod != http.MethodPost || gotBody != `{"hi":1}` || gotHeader != "abc" {
		t.Errorf("upstream saw path=%q method=%q body=%q x-test=%q", gotPath, gotMethod, gotBody, gotHeader)
	}
	if resp.StatusCode != http.StatusMultiStatus || string(b) != "upstream-body" || resp.Header.Get("X-Up") != "1" {
		t.Errorf("client got status=%d body=%q x-up=%q", resp.StatusCode, b, resp.Header.Get("X-Up"))
	}
	// Transparent/invisible: the proxy must not announce itself upstream.
	if gotXFF != "" || gotVia != "" {
		t.Errorf("proxy leaked identity headers upstream: X-Forwarded-For=%q Via=%q", gotXFF, gotVia)
	}
}

// TestGatewayStreaming proves responses are streamed (flushed per write) rather
// than buffered: the client must receive the first chunk while the upstream is
// still blocked from sending the second.
func TestGatewayStreaming(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("upstream ResponseWriter is not a Flusher")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "event: a\n\n")
		fl.Flush()
		<-release // hold the second chunk until the client has read the first
		_, _ = io.WriteString(w, "event: b\n\n")
		fl.Flush()
	}))
	defer up.Close()
	srv := httptest.NewServer(newTestGateway(t, up.URL))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/messages")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 64)
	type readResult struct {
		n   int
		err error
	}
	ch := make(chan readResult, 1)
	go func() {
		n, err := resp.Body.Read(buf)
		ch <- readResult{n, err}
	}()
	select {
	case r := <-ch:
		close(release)
		if r.n == 0 || !strings.Contains(string(buf[:r.n]), "event: a") {
			t.Fatalf("first chunk not streamed: n=%d err=%v got=%q", r.n, r.err, buf[:r.n])
		}
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("first chunk not received within 3s — response is being buffered, not streamed")
	}

	rest, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(rest), "event: b") {
		t.Errorf("second chunk missing from stream: %q", rest)
	}
}

// count_tokens is forwarded when the upstream supports it; the gateway also
// learns and caches that capability. (The missing→local-tokenizer branch needs
// Node + ai-tokenizer and is covered by the end-to-end smoke test, not here.)
func TestGatewayCountTokensPassthrough(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages/count_tokens" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"input_tokens":42}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer up.Close()
	gw := newTestGateway(t, up.URL)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/messages/count_tokens", "application/json",
		strings.NewReader(`{"model":"claude-x","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"input_tokens":42`) {
		t.Errorf("count_tokens passthrough body = %s", b)
	}
	if resp.Header.Get("X-Ccgate-Count") == "local" {
		t.Error("expected upstream passthrough, got local count")
	}
	if cap := gw.CountTokens().Capability(); string(cap) != "supported" {
		t.Errorf("learned capability = %q, want supported", cap)
	}
}
