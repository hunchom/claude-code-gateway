// Package config loads, merges, and validates gateway configuration.
//
// Values resolve in increasing order of precedence: built-in defaults, then an
// optional YAML file, then environment variables (CCGW_*). Secrets such as the
// PKCS#12 password are accepted only from the environment and are never written
// back to disk.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// count_tokens handling modes.
const (
	CountAuto        = "auto"        // probe upstream, cache result, recheck periodically
	CountLocal       = "local"       // always count locally with ai-tokenizer
	CountPassthrough = "passthrough" // always forward to upstream
)

// Config is the fully resolved gateway configuration.
type Config struct {
	Listen   string `yaml:"listen"`   // local address Claude Code connects to
	Upstream string `yaml:"upstream"` // Anthropic-compatible endpoint to forward to

	Model          string `yaml:"model"`           // optional model id passed through/defaulted
	TokenizerModel string `yaml:"tokenizer_model"` // ai-tokenizer model key for local counting

	P12Path     string `yaml:"p12_path"`  // password-protected client certificate bundle
	P12Password string `yaml:"-"`         // env only (CCGW_P12_PASSWORD); never serialized
	CABundle    string `yaml:"ca_bundle"` // optional extra CA PEM file, added to the embedded bundle

	CountTokens  string `yaml:"count_tokens"`   // auto | local | passthrough
	RecheckHours int    `yaml:"recheck_hours"`  // upstream capability recheck cadence
	ImageTokens  int    `yaml:"image_tokens"`   // flat token estimate per image block
	PDFTokens    int    `yaml:"pdf_tokens"`     // flat token estimate per pdf document block
	PoolSize     int    `yaml:"tokenizer_pool"` // node tokenizer worker count
	NodeBin      string `yaml:"node_bin"`       // node executable (default: node on PATH)

	CountTimeout int               `yaml:"count_timeout"` // upstream count_tokens timeout (seconds)
	ModelMap     map[string]string `yaml:"model_map"`     // explicit request model -> ai-tokenizer key overrides

	// Resolved at runtime; never read from YAML.
	ConfigDir  string `yaml:"-"`
	StateDir   string `yaml:"-"`
	SidecarDir string `yaml:"-"`
}

// Default returns a Config populated with safe defaults.
func Default() *Config {
	dir := configDir()
	return &Config{
		Listen:         "127.0.0.1:8787",
		Upstream:       "https://api.anthropic.com",
		TokenizerModel: "anthropic/claude-sonnet-4.5",
		CountTokens:    CountAuto,
		RecheckHours:   6,
		ImageTokens:    1600,
		PDFTokens:      3000,
		PoolSize:       4,
		NodeBin:        "node",
		CountTimeout:   30,
		ConfigDir:      dir,
		StateDir:       dir,
		SidecarDir:     filepath.Join(dir, "tokenizer"),
	}
}

// Load reads configuration from path (ignored if absent), applies environment
// overrides, resolves runtime paths, and returns the merged Config.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("parse config %s: %w", path, err)
			}
		case os.IsNotExist(err):
			// defaults + env only
		default:
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	}
	cfg.applyEnv()
	cfg.resolvePaths()
	return cfg, nil
}

func (c *Config) applyEnv() {
	setStr(&c.Listen, "CCGW_LISTEN")
	setStr(&c.Upstream, "CCGW_UPSTREAM")
	setStr(&c.Model, "CCGW_MODEL")
	setStr(&c.TokenizerModel, "CCGW_TOKENIZER_MODEL")
	setStr(&c.P12Path, "CCGW_P12_PATH")
	setStr(&c.P12Password, "CCGW_P12_PASSWORD")
	setStr(&c.CABundle, "CCGW_CA_BUNDLE")
	setStr(&c.CountTokens, "CCGW_COUNT_TOKENS")
	setStr(&c.NodeBin, "CCGW_NODE_BIN")
	setInt(&c.RecheckHours, "CCGW_RECHECK_HOURS")
	setInt(&c.ImageTokens, "CCGW_IMAGE_TOKENS")
	setInt(&c.PDFTokens, "CCGW_PDF_TOKENS")
	setInt(&c.PoolSize, "CCGW_TOKENIZER_POOL")
	setInt(&c.CountTimeout, "CCGW_COUNT_TIMEOUT")
}

func (c *Config) resolvePaths() {
	if c.ConfigDir == "" {
		c.ConfigDir = configDir()
	}
	if c.StateDir == "" {
		c.StateDir = c.ConfigDir
	}
	if c.SidecarDir == "" {
		c.SidecarDir = filepath.Join(c.ConfigDir, "tokenizer")
	}
}

// Validate checks the configuration for internal consistency, normalizing a few
// out-of-range numeric fields in place.
func (c *Config) Validate() error {
	if c.Listen == "" {
		return fmt.Errorf("listen address is empty")
	}
	if c.Upstream == "" {
		return fmt.Errorf("upstream endpoint is empty")
	}
	if !strings.HasPrefix(c.Upstream, "http://") && !strings.HasPrefix(c.Upstream, "https://") {
		return fmt.Errorf("upstream must be an http(s) URL: %q", c.Upstream)
	}
	switch c.CountTokens {
	case CountAuto, CountLocal, CountPassthrough:
	default:
		return fmt.Errorf("count_tokens must be auto|local|passthrough, got %q", c.CountTokens)
	}
	if c.RecheckHours <= 0 {
		c.RecheckHours = 6
	}
	if c.PoolSize <= 0 {
		c.PoolSize = 1
	}
	if c.CountTimeout <= 0 {
		c.CountTimeout = 30
	}
	return nil
}

// DefaultConfigPath returns the conventional config file location.
func DefaultConfigPath() string {
	return filepath.Join(configDir(), "config.yaml")
}

func configDir() string {
	if d := os.Getenv("CCGW_CONFIG_DIR"); d != "" {
		return d
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "claude-code-gateway")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "claude-code-gateway")
	}
	return filepath.Join(home, ".config", "claude-code-gateway")
}

func setStr(dst *string, env string) {
	if v := os.Getenv(env); v != "" {
		*dst = v
	}
}

func setInt(dst *int, env string) {
	if v := os.Getenv(env); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}
