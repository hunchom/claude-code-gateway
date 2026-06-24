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
