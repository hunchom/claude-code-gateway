// Package counttokens serves Anthropic /v1/messages/count_tokens requests.
//
// When the upstream endpoint implements count_tokens the request is forwarded
// unchanged. When it does not, the request is converted to the ai-tokenizer SDK
// shape and counted locally by a pool of Node worker processes. The upstream
// capability is probed once, cached to disk, and rechecked periodically and on
// every launch so the gateway adapts without manual configuration.
package counttokens

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hunchom/claude-code-gateway/internal/config"
	"github.com/hunchom/claude-code-gateway/internal/state"
)

// Options configures a Service.
type Options struct {
	Mode         string // config.CountAuto | CountLocal | CountPassthrough
	Upstream     string
	RecheckHours int
	StateDir     string

	TokenizerModel string
	SidecarDir     string
	NodeBin        string
	PoolSize       int
	ImageTokens    int
	PDFTokens      int
}

// IsCountTokensPath reports whether p is one of the count_tokens routes Claude
// Code may use (with or without a routing prefix).
func IsCountTokensPath(p string) bool {
	switch p {
	case "/v1/messages/count_tokens",
		"/anthropic/v1/messages/count_tokens",
		"/messages/count_tokens":
		return true
	}
	return strings.HasSuffix(p, "/v1/messages/count_tokens")
}

// Service decides, per request, whether to forward count_tokens upstream or
// answer it locally, and remembers what it learns.
type Service struct {
	opts     Options
	upstream string
	client   *http.Client

	mu     sync.RWMutex
	st     state.State
	forced bool

	poolOnce sync.Once
	pool     *Pool
	poolErr  error
}

// NewService builds a Service. The local tokenizer pool is created lazily on the
// first local count, so an upstream that supports count_tokens needs no Node.
func NewService(opts Options, client *http.Client) *Service {
	if opts.RecheckHours <= 0 {
		opts.RecheckHours = 6
	}
	s := &Service{
		opts:     opts,
		upstream: strings.TrimRight(opts.Upstream, "/"),
		client:   client,
	}
	s.st = state.Load(opts.StateDir)
	return s
}

// Capability returns the last known upstream capability.
func (s *Service) Capability() state.Capability {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.st.CountTokens
}

// Status is a snapshot of the count_tokens service for the operator status route.
type Status struct {
	Mode                string `json:"mode"`
	Upstream            string `json:"upstream"`
	CountTokensUpstream string `json:"count_tokens_upstream"`
	CheckedAt           string `json:"checked_at,omitempty"`
}

// Status returns the current service state for /_ccgate/status.
func (s *Service) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	checked := ""
	if !s.st.CheckedAt.IsZero() {
		checked = s.st.CheckedAt.UTC().Format(time.RFC3339)
	}
	return Status{
		Mode:                s.opts.Mode,
		Upstream:            s.upstream,
		CountTokensUpstream: string(s.st.CountTokens),
		CheckedAt:           checked,
	}
}

// ForceRecheck marks the cached capability stale so the next request re-probes
// upstream. Called on launch and on the periodic timer.
func (s *Service) ForceRecheck() {
	s.mu.Lock()
	s.forced = true
	s.mu.Unlock()
}

// StartRechecker arms a recheck every RecheckHours until ctx is cancelled.
func (s *Service) StartRechecker(ctx context.Context) {
	d := time.Duration(s.opts.RecheckHours) * time.Hour
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.ForceRecheck()
		}
	}
}

// Handle answers a count_tokens request.
func (s *Service) Handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	_ = r.Body.Close()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "could not read request body")
		return
	}

	switch s.decide() {
	case decidePassthrough:
		if !s.forwardUpstream(w, r, body) {
			s.serveLocal(w, body)
		}
	case decideProbe:
		decided, supported, status, rb, hdr := s.probeUpstream(r, body)
		if decided && supported {
			s.record(state.Supported)
			writeRaw(w, status, hdr, rb)
			return
		}
		if decided && !supported {
			s.record(state.Unsupported)
		}
		s.serveLocal(w, body)
	default:
		s.serveLocal(w, body)
	}
}

type decision int

const (
	decideLocal decision = iota
	decidePassthrough
	decideProbe
)

func (s *Service) decide() decision {
	switch s.opts.Mode {
	case config.CountLocal:
		return decideLocal
	case config.CountPassthrough:
		return decidePassthrough
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	fresh := !s.forced &&
		s.st.Endpoint == s.upstream &&
		time.Since(s.st.CheckedAt) < time.Duration(s.opts.RecheckHours)*time.Hour
	if fresh {
		switch s.st.CountTokens {
		case state.Supported:
			return decidePassthrough
		case state.Unsupported:
			return decideLocal
		}
	}
	return decideProbe
}

func (s *Service) record(c state.Capability) {
	s.mu.Lock()
	s.st = state.State{Endpoint: s.upstream, CountTokens: c, CheckedAt: time.Now()}
	s.forced = false
	st := s.st
	s.mu.Unlock()
	if err := state.Save(s.opts.StateDir, st); err != nil {
		log.Printf("count_tokens: persist state: %v", err)
	}
	log.Printf("count_tokens: upstream capability = %s", c)
}

// forwardUpstream proxies the request to the upstream count_tokens endpoint and
// copies the response back. It returns false only on a transport error, leaving
// the caller free to fall back to local counting.
func (s *Service) forwardUpstream(w http.ResponseWriter, r *http.Request, body []byte) bool {
	resp, err := s.doUpstream(r, body)
	if err != nil {
		log.Printf("count_tokens: upstream error: %v", err)
		return false
	}
	defer resp.Body.Close()
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	return true
}

// probeUpstream forwards the request and classifies the upstream's support for
// count_tokens. decided is false when the result is ambiguous (transport error
// or an unexpected status) and the cache should not be updated.
func (s *Service) probeUpstream(r *http.Request, body []byte) (decided, supported bool, status int, respBody []byte, hdr http.Header) {
	resp, err := s.doUpstream(r, body)
	if err != nil {
		return false, false, 0, nil, nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch Classify(resp.StatusCode, b) {
	case state.Supported:
		return true, true, resp.StatusCode, b, resp.Header
	case state.Unsupported:
		return true, false, resp.StatusCode, b, resp.Header
	default:
		return false, false, resp.StatusCode, b, resp.Header
	}
}

// Classify maps an upstream count_tokens response to a capability: Supported
// (200 carrying input_tokens), Unsupported (404/405/501 or a not_found_error
// body), or Unknown for anything ambiguous (auth failures, 5xx, odd bodies).
// Shared by the request-path probe and the doctor command.
func Classify(status int, body []byte) state.Capability {
	switch {
	case status == http.StatusOK && bytes.Contains(body, []byte("input_tokens")):
		return state.Supported
	case status == http.StatusNotFound,
		status == http.StatusMethodNotAllowed,
		status == http.StatusNotImplemented,
		bytes.Contains(body, []byte("not_found_error")):
		return state.Unsupported
	default:
		return state.Unknown
	}
}

func (s *Service) newUpstreamRequest(r *http.Request, body []byte) (*http.Request, error) {
	u := s.upstream + r.URL.Path
	if r.URL.RawQuery != "" {
		u += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	copyHeader(req.Header, r.Header)
	req.Header.Del("Accept-Encoding") // we buffer this small response; avoid compressed body
	req.ContentLength = int64(len(body))
	return req, nil
}

// doUpstream forwards the buffered count_tokens request, retrying transient
// failures (network errors and 5xx except 501) with small exponential backoff.
// Safe to retry because the body is fully buffered and count_tokens is
// idempotent. 501 is never retried — it is a definitive "unsupported" signal.
func (s *Service) doUpstream(r *http.Request, body []byte) (*http.Response, error) {
	const attempts = 3
	var lastErr error
	for i := range attempts {
		if i > 0 {
			select {
			case <-r.Context().Done():
				return nil, r.Context().Err()
			case <-time.After(time.Duration(50<<i) * time.Millisecond):
			}
		}
		req, err := s.newUpstreamRequest(r, body)
		if err != nil {
			return nil, err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= 500 && resp.StatusCode != http.StatusNotImplemented && i < attempts-1 {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("upstream returned %d", resp.StatusCode)
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

func (s *Service) serveLocal(w http.ResponseWriter, body []byte) {
	var req anthropicCountRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON: "+err.Error())
		return
	}
	pool, err := s.ensurePool()
	if err != nil {
		writeErr(w, http.StatusBadGateway, "api_error", "local tokenizer unavailable: "+err.Error())
		return
	}
	sdkReq, err := convertToSDK(&req, s.countConfig())
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	total, err := pool.Count(context.Background(), sdkReq)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "api_error", "token counting failed: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Ccgate-Count", "local")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]int{"input_tokens": total})
}

func (s *Service) ensurePool() (*Pool, error) {
	s.poolOnce.Do(func() {
		s.pool, s.poolErr = NewPool(s.countConfig())
	})
	return s.pool, s.poolErr
}

func (s *Service) countConfig() *CountConfig {
	return &CountConfig{
		Model:      s.opts.TokenizerModel,
		SidecarDir: s.opts.SidecarDir,
		NodeBin:    s.opts.NodeBin,
		BootWait:   60 * time.Second,
		CountWait:  20 * time.Second,
		PoolSize:   s.opts.PoolSize,
		ImageTok:   s.opts.ImageTokens,
		PDFTok:     s.opts.PDFTokens,
	}
}

// Close releases tokenizer workers.
func (s *Service) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func writeErr(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":  "error",
		"error": map[string]string{"type": typ, "message": msg},
	})
}

func writeRaw(w http.ResponseWriter, status int, hdr http.Header, body []byte) {
	copyHeader(w.Header(), hdr)
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// hopByHop headers are connection-scoped and must not be copied across a proxy.
var hopByHop = map[string]bool{
	"Connection":          true,
	"Proxy-Connection":    true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		if hopByHop[k] {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// ===========================================================================
// Tokenizer worker pool
// ===========================================================================

// CountConfig holds everything the local tokenizer needs.
type CountConfig struct {
	Model      string
	SidecarDir string
	NodeBin    string
	BootWait   time.Duration
	CountWait  time.Duration
	PoolSize   int
	ImageTok   int
	PDFTok     int
}

// worker is one Node process speaking NDJSON, guarded by its own mutex so a
// single process is never used by two requests at once.
type worker struct {
	cfg    *CountConfig
	id     int
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	nextID int64

	// dead is replaced on every (re)start; the goroutine running cmd.Wait sets
	// the value it captured, so a stale reaper cannot mark a fresh process dead.
	dead atomic.Pointer[atomic.Bool]
}

func newWorker(cfg *CountConfig, id int) (*worker, error) {
	w := &worker{cfg: cfg, id: id}
	if err := w.start(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *worker) start() error {
	script := filepath.Join(w.cfg.SidecarDir, "tokenizer-sidecar.mjs")
	c := exec.Command(w.cfg.NodeBin, script)
	c.Dir = w.cfg.SidecarDir
	c.Env = append(os.Environ(), "COUNT_TOKENS_MODEL="+w.cfg.Model)
	c.Stderr = os.Stderr

	stdin, err := c.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		return err
	}
	if err := c.Start(); err != nil {
		return fmt.Errorf("start node worker (node installed?): %w", err)
	}
	w.cmd = c
	w.stdin = stdin
	w.stdout = bufio.NewReaderSize(stdout, 1<<20)

	// Reap the process so it never becomes a zombie, and track liveness.
	d := &atomic.Bool{}
	w.dead.Store(d)
	go func(cmd *exec.Cmd, flag *atomic.Bool) {
		_ = cmd.Wait()
		flag.Store(true)
	}(c, d)

	line, err := w.readLineTimeout(w.cfg.BootWait)
	if err != nil {
		w.kill()
		return fmt.Errorf("worker %d handshake: %w", w.id, err)
	}
	var r struct {
		Ready bool   `json:"ready"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(line, &r); err != nil {
		w.kill()
		return fmt.Errorf("worker %d handshake decode: %w (line=%q)", w.id, err, string(line))
	}
	if !r.Ready {
		w.kill()
		return fmt.Errorf("worker %d init failed: %s", w.id, r.Error)
	}
	return nil
}

func (w *worker) kill() {
	if w.cmd != nil && w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
	}
}

func (w *worker) alive() bool {
	d := w.dead.Load()
	return d != nil && !d.Load()
}

func (w *worker) readLineTimeout(d time.Duration) ([]byte, error) {
	type res struct {
		line []byte
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		l, e := w.stdout.ReadBytes('\n')
		ch <- res{l, e}
	}()
	select {
	case r := <-ch:
		return bytes.TrimSpace(r.line), r.err
	case <-time.After(d):
		return nil, errors.New("timeout")
	}
}

// count runs one request on this worker, restarting it once on a pipe error.
func (w *worker) count(req *sdkRequest) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	total, err := w.countLocked(req)
	if err != nil && isPipeErr(err) {
		log.Printf("worker %d pipe error (%v); restarting", w.id, err)
		w.kill()
		if rerr := w.start(); rerr != nil {
			return 0, rerr
		}
		return w.countLocked(req)
	}
	return total, err
}

func (w *worker) countLocked(req *sdkRequest) (int, error) {
	w.nextID++
	id := w.nextID
	env := struct {
		ID int64 `json:"id"`
		*sdkRequest
	}{ID: id, sdkRequest: req}
	payload, err := json.Marshal(env)
	if err != nil {
		return 0, err
	}
	payload = append(payload, '\n')
	if _, err := w.stdin.Write(payload); err != nil {
		return 0, err
	}

	type res struct {
		total int
		err   error
	}
	ch := make(chan res, 1)
	go func() {
		for {
			line, err := w.stdout.ReadBytes('\n')
			if err != nil {
				ch <- res{0, err}
				return
			}
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var rep struct {
				ID    int64  `json:"id"`
				Total int    `json:"total"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(line, &rep); err != nil {
				continue
			}
			if rep.ID != id {
				continue
			}
			if rep.Error != "" {
				ch <- res{0, fmt.Errorf("worker: %s", rep.Error)}
				return
			}
			ch <- res{rep.Total, nil}
			return
		}
	}()
	select {
	case r := <-ch:
		return r.total, r.err
	case <-time.After(w.cfg.CountWait):
		w.kill() // wedged; pool replaces it on return
		return 0, errors.New("count timeout")
	}
}

// Pool is a channel of warm workers giving true parallelism up to PoolSize.
type Pool struct {
	cfg     *CountConfig
	workers chan *worker
	all     []*worker
	mu      sync.Mutex
	closed  bool
}

// NewPool installs the sidecar (and runs npm install on first use) then starts
// PoolSize warm Node workers.
func NewPool(cfg *CountConfig) (*Pool, error) {
	if cfg.PoolSize < 1 {
		cfg.PoolSize = 1
	}
	if err := ensureSidecar(cfg); err != nil {
		return nil, err
	}
	p := &Pool{cfg: cfg, workers: make(chan *worker, cfg.PoolSize)}
	for i := 0; i < cfg.PoolSize; i++ {
		w, err := newWorker(cfg, i)
		if err != nil {
			p.Close()
			return nil, err
		}
		p.all = append(p.all, w)
		p.workers <- w
	}
	log.Printf("count_tokens: tokenizer pool ready (%d workers, model=%s)", cfg.PoolSize, cfg.Model)
	return p, nil
}

// Count checks out a worker, runs the count, and returns the worker — replacing
// it if it died so the pool never shrinks.
func (p *Pool) Count(ctx context.Context, req *sdkRequest) (int, error) {
	var w *worker
	select {
	case w = <-p.workers:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	defer func() {
		if w.alive() {
			p.workers <- w
			return
		}
		if nw, err := newWorker(p.cfg, w.id); err == nil {
			p.mu.Lock()
			for i, ow := range p.all {
				if ow == w {
					p.all[i] = nw
				}
			}
			p.mu.Unlock()
			p.workers <- nw
		} else {
			log.Printf("count_tokens: replace dead worker %d: %v", w.id, err)
			p.workers <- w // keep the slot; next use restarts it
		}
	}()
	return w.count(req)
}

// Close kills every worker.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	for _, w := range p.all {
		w.kill()
	}
}

func ensureSidecar(cfg *CountConfig) error {
	if err := writeSidecarFiles(cfg.SidecarDir); err != nil {
		return fmt.Errorf("write sidecar: %w", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.SidecarDir, "node_modules", "ai-tokenizer")); err == nil {
		return nil
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		return fmt.Errorf("npm not found on PATH (needed to install ai-tokenizer): %w", err)
	}
	log.Printf("count_tokens: installing ai-tokenizer in %s ...", cfg.SidecarDir)
	cmd := exec.Command(npm, "install", "--silent", "--no-audit", "--no-fund")
	cmd.Dir = cfg.SidecarDir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("npm install failed: %w", err)
	}
	return nil
}

func writeSidecarFiles(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	files := map[string]string{
		"tokenizer-sidecar.mjs": sidecarJS,
		"package.json":          sidecarPkg,
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if existing, err := os.ReadFile(p); err == nil && string(existing) == content {
			continue
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func isPipeErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, io.EOF) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") || strings.Contains(msg, "file already closed")
}
