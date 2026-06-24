package config

import (
	"os"
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
