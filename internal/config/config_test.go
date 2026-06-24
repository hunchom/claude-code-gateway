package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultValidates(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("CCGW_UPSTREAM", "https://example.test")
	t.Setenv("CCGW_TOKENIZER_POOL", "9")
	t.Setenv("CCGW_COUNT_TOKENS", "local")

	c, err := Load(os.DevNull) // not a YAML file; treated as empty → defaults + env
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Upstream != "https://example.test" {
		t.Errorf("upstream = %q", c.Upstream)
	}
	if c.PoolSize != 9 {
		t.Errorf("pool = %d, want 9", c.PoolSize)
	}
	if c.CountTokens != "local" {
		t.Errorf("count_tokens = %q", c.CountTokens)
	}
}

func TestCountTimeout(t *testing.T) {
	if Default().CountTimeout != 30 {
		t.Errorf("default count_timeout = %d, want 30", Default().CountTimeout)
	}
	t.Setenv("CCGW_COUNT_TIMEOUT", "12")
	c, err := Load(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	if c.CountTimeout != 12 {
		t.Errorf("env count_timeout = %d, want 12", c.CountTimeout)
	}
}

func TestLoadYAMLFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := `
listen: 127.0.0.1:9999
upstream: https://up.example
tokenizer_model: anthropic/claude-haiku-4.5
count_tokens: local
recheck_hours: 12
tokenizer_pool: 7
count_timeout: 45
model_map:
  my-alias: anthropic/claude-sonnet-4.5
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9999" || c.Upstream != "https://up.example" {
		t.Errorf("listen=%q upstream=%q", c.Listen, c.Upstream)
	}
	if c.TokenizerModel != "anthropic/claude-haiku-4.5" || c.CountTokens != "local" {
		t.Errorf("tokenizer_model=%q count_tokens=%q", c.TokenizerModel, c.CountTokens)
	}
	if c.RecheckHours != 12 || c.PoolSize != 7 || c.CountTimeout != 45 {
		t.Errorf("recheck=%d pool=%d timeout=%d", c.RecheckHours, c.PoolSize, c.CountTimeout)
	}
	if c.ModelMap["my-alias"] != "anthropic/claude-sonnet-4.5" {
		t.Errorf("model_map not parsed: %v", c.ModelMap)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
}

func TestValidateRejectsBadUpstream(t *testing.T) {
	c := Default()
	c.Upstream = "ftp://nope"
	if err := c.Validate(); err == nil {
		t.Error("expected error for non-http upstream")
	}
}

func TestValidateRejectsBadMode(t *testing.T) {
	c := Default()
	c.CountTokens = "bogus"
	if err := c.Validate(); err == nil {
		t.Error("expected error for invalid count_tokens mode")
	}
}
