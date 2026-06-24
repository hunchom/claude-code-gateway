// Package proxy is the gateway HTTP front end: a transparent reverse proxy to
// the upstream endpoint, with count_tokens routed to a capability-aware service.
//
// Everything except count_tokens is forwarded byte-for-byte with streaming
// flushes, so new request/response fields introduced by Claude Code upgrades
// pass through untouched.
package proxy

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/hunchom/claude-code-gateway/internal/config"
	"github.com/hunchom/claude-code-gateway/internal/counttokens"
)

// Gateway routes incoming Claude Code traffic.
type Gateway struct {
	cfg *config.Config
	rp  *httputil.ReverseProxy
	ct  *counttokens.Service
}

// New builds a Gateway. tlsCfg is applied to the upstream transport (client
// certificate + trust roots).
func New(cfg *config.Config, tlsCfg *tls.Config) (*Gateway, error) {
	target, err := url.Parse(cfg.Upstream)
	if err != nil {
		return nil, fmt.Errorf("parse upstream %q: %w", cfg.Upstream, err)
	}

	transport := &http.Transport{
		TLSClientConfig:       tlsCfg,
		ForceAttemptHTTP2:     true,
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target) // scheme + host + path join
			// Match Host header + TLS SNI to the upstream so name-based routing
			// and certificate verification work. No X-Forwarded-* is added, so
			// the request looks like it came straight from Claude Code.
			pr.Out.Host = target.Host
		},
		Transport:     transport,
		FlushInterval: -1, // flush each write immediately for SSE streaming
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proxy: %s %s: %v", r.Method, r.URL.Path, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"upstream unavailable"}}`))
		},
	}

	ct := counttokens.NewService(counttokens.Options{
		Mode:           cfg.CountTokens,
		Upstream:       cfg.Upstream,
		RecheckHours:   cfg.RecheckHours,
		StateDir:       cfg.StateDir,
		TokenizerModel: cfg.TokenizerModel,
		SidecarDir:     cfg.SidecarDir,
		NodeBin:        cfg.NodeBin,
		PoolSize:       cfg.PoolSize,
		ImageTokens:    cfg.ImageTokens,
		PDFTokens:      cfg.PDFTokens,
	}, &http.Client{Transport: transport, Timeout: 30 * time.Second})

	return &Gateway{cfg: cfg, rp: rp, ct: ct}, nil
}

// ServeHTTP routes count_tokens to the capability-aware service and everything
// else through the transparent reverse proxy.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if counttokens.IsCountTokensPath(r.URL.Path) {
		g.ct.Handle(w, r)
		return
	}
	g.rp.ServeHTTP(w, r)
}

// CountTokens exposes the count_tokens service for lifecycle control.
func (g *Gateway) CountTokens() *counttokens.Service { return g.ct }
