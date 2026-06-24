// Command ccgate is a transparent gateway and launcher for Claude Code.
//
// It sits between Claude Code and any Anthropic-compatible endpoint (a LiteLLM
// proxy, Amazon Bedrock gateway, or api.anthropic.com), authenticating upstream
// with a client certificate extracted from a password-protected PKCS#12 bundle,
// forwarding every request unchanged, and answering /v1/messages/count_tokens
// locally when the upstream does not implement it.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"embed"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hunchom/claude-code-gateway/internal/config"
	"github.com/hunchom/claude-code-gateway/internal/mtls"
	"github.com/hunchom/claude-code-gateway/internal/proxy"
	"github.com/hunchom/claude-code-gateway/internal/state"
	"golang.org/x/term"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "0.1.0-dev"

//go:embed all:certs/ca
var caFS embed.FS

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	args := os.Args[2:]
	switch os.Args[1] {
	case "run":
		cmdRun(args)
	case "claude":
		cmdClaude(args)
	case "setup":
		cmdSetup(args)
	case "doctor":
		cmdDoctor(args)
	case "version", "-v", "--version":
		fmt.Println("ccgate", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `ccgate — transparent gateway and launcher for Claude Code

Usage:
  ccgate run                 Run the gateway in the foreground
  ccgate claude [args...]    Launch Claude Code through the gateway
  ccgate setup               Extract user-cert.pem / user-key.pem from a .p12
  ccgate doctor              Diagnose configuration and connectivity
  ccgate version             Print version

Configuration is read from (lowest to highest precedence):
  defaults, the --config file, then CCGW_* environment variables.
Default config path: `+config.DefaultConfigPath()+`

Common environment variables:
  CCGW_UPSTREAM           upstream endpoint URL
  CCGW_LISTEN             local listen address (default 127.0.0.1:8787)
  CCGW_P12_PATH           path to the client .p12
  CCGW_P12_PASSWORD       password for the .p12 (never written to disk)
  CCGW_TOKENIZER_MODEL    ai-tokenizer model key for local counting
  CCGW_COUNT_TOKENS       auto | local | passthrough
`)
}

func mustConfig(args []string, name string) (*config.Config, []string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	var path string
	fs.StringVar(&path, "config", config.DefaultConfigPath(), "config file path")
	_ = fs.Parse(args)
	cfg, err := config.Load(path)
	if err != nil {
		fatal("config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		fatal("config: %v", err)
	}
	return cfg, fs.Args()
}

func buildGateway(cfg *config.Config) *proxy.Gateway {
	tlsCfg, err := buildTLS(cfg)
	if err != nil {
		fatal("tls: %v", err)
	}
	gw, err := proxy.New(cfg, tlsCfg)
	if err != nil {
		fatal("gateway: %v", err)
	}
	return gw
}

func buildTLS(cfg *config.Config) (*tls.Config, error) {
	var certPtr *tls.Certificate
	if cfg.P12Path != "" {
		cert, err := mtls.LoadClientCertificate(cfg.P12Path, cfg.P12Password)
		if err != nil {
			return nil, err
		}
		certPtr = &cert
	}
	return mtls.BuildTLSConfig(certPtr, embeddedCA(), cfg.CABundle)
}

func embeddedCA() []byte {
	var buf bytes.Buffer
	entries, err := caFS.ReadDir("certs/ca")
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pem") {
			continue
		}
		if b, err := caFS.ReadFile("certs/ca/" + e.Name()); err == nil {
			buf.Write(b)
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}

// cmdRun runs the gateway in the foreground until interrupted.
func cmdRun(args []string) {
	cfg, _ := mustConfig(args, "run")
	gw := buildGateway(cfg)

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		fatal("listen %s: %v", cfg.Listen, err)
	}
	srv := &http.Server{Handler: gw}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	gw.CountTokens().ForceRecheck()
	go gw.CountTokens().StartRechecker(ctx)

	fmt.Fprintf(os.Stderr, "ccgate %s listening on http://%s -> %s\n", version, ln.Addr(), cfg.Upstream)
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		gw.CountTokens().Close()
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fatal("serve: %v", err)
	}
}

// cmdClaude binds the gateway, points Claude Code at it, and execs claude.
func cmdClaude(args []string) {
	cfg, rest := mustConfig(args, "claude")
	gw := buildGateway(cfg)

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		fatal("listen %s: %v", cfg.Listen, err)
	}
	srv := &http.Server{Handler: gw}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "ccgate: serve: %v\n", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gw.CountTokens().ForceRecheck()         // capability check on every launch
	go gw.CountTokens().StartRechecker(ctx) // recheck every RecheckHours while running

	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		fatal("claude not found on PATH: %v", err)
	}
	baseURL := "http://" + ln.Addr().String()
	c := exec.Command(claudeBin, rest...)
	c.Env = append(os.Environ(), "ANTHROPIC_BASE_URL="+baseURL)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	fmt.Fprintf(os.Stderr, "ccgate %s: ANTHROPIC_BASE_URL=%s -> %s\n", version, baseURL, cfg.Upstream)

	runErr := c.Run()
	cancel()
	gw.CountTokens().Close()
	shutCtx, sc := context.WithTimeout(context.Background(), 3*time.Second)
	defer sc()
	_ = srv.Shutdown(shutCtx)
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fatal("claude: %v", runErr)
	}
}

// cmdSetup extracts PEM files from the configured (or flagged) .p12.
func cmdSetup(args []string) {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	var configPath, p12, certOut, keyOut string
	fs.StringVar(&configPath, "config", config.DefaultConfigPath(), "config file path")
	fs.StringVar(&p12, "p12", "", "path to the .p12 (default: config p12_path)")
	fs.StringVar(&certOut, "cert-out", "user-cert.pem", "certificate output path")
	fs.StringVar(&keyOut, "key-out", "user-key.pem", "private key output path")
	_ = fs.Parse(args)

	cfg, err := config.Load(configPath)
	if err != nil {
		fatal("config: %v", err)
	}
	if p12 == "" {
		p12 = cfg.P12Path
	}
	if p12 == "" {
		fatal("no .p12 path (pass --p12 or set CCGW_P12_PATH / p12_path)")
	}
	password := cfg.P12Password
	if password == "" {
		password = promptPassword(fmt.Sprintf("Password for %s: ", p12))
	}
	if err := mtls.ExtractPEM(p12, password, certOut, keyOut); err != nil {
		fatal("%v", err)
	}
	fmt.Printf("Wrote %s and %s\n", certOut, keyOut)
}

// cmdDoctor reports on configuration, certificate material, and connectivity.
func cmdDoctor(args []string) {
	cfg, _ := mustConfig(args, "doctor")
	fmt.Printf("ccgate %s\n", version)
	fmt.Printf("config dir:      %s\n", cfg.ConfigDir)
	fmt.Printf("upstream:        %s\n", cfg.Upstream)
	fmt.Printf("listen:          %s\n", cfg.Listen)
	fmt.Printf("count_tokens:    %s\n", cfg.CountTokens)
	fmt.Printf("tokenizer model: %s\n", cfg.TokenizerModel)

	st := state.Load(cfg.StateDir)
	fmt.Printf("learned upstream count_tokens: %s (checked %s)\n", st.CountTokens, humanTime(st.CheckedAt))

	if cfg.P12Path == "" {
		fmt.Println("client cert:     none configured (no mTLS)")
	} else if _, err := mtls.LoadClientCertificate(cfg.P12Path, cfg.P12Password); err != nil {
		fmt.Printf("client cert:     ERROR %v\n", err)
	} else {
		fmt.Printf("client cert:     OK (%s)\n", cfg.P12Path)
	}

	if _, err := exec.LookPath("node"); err != nil {
		fmt.Println("node:            NOT FOUND (local count_tokens unavailable)")
	} else {
		fmt.Println("node:            OK")
	}

	tlsCfg, err := buildTLS(cfg)
	if err != nil {
		fmt.Printf("tls config:      ERROR %v\n", err)
		return
	}
	if u := cfg.Upstream; strings.HasPrefix(u, "https://") {
		host := strings.TrimPrefix(u, "https://")
		if i := strings.IndexAny(host, "/"); i >= 0 {
			host = host[:i]
		}
		if !strings.Contains(host, ":") {
			host += ":443"
		}
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", host, tlsCfg)
		if err != nil {
			fmt.Printf("upstream tls:    ERROR %v\n", err)
		} else {
			_ = conn.Close()
			fmt.Printf("upstream tls:    OK (%s)\n", host)
		}
	}
}

func promptPassword(prompt string) string {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		fatal("read password: %v", err)
	}
	return string(b)
}

func humanTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Format(time.RFC3339)
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "ccgate: "+format+"\n", a...)
	os.Exit(1)
}
