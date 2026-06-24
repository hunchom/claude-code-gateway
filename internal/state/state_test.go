package state

import (
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := State{Endpoint: "https://up", CountTokens: Supported, CheckedAt: time.Now().Truncate(time.Second)}
	if err := Save(dir, in); err != nil {
		t.Fatal(err)
	}
	out := Load(dir)
	if out.Endpoint != in.Endpoint || out.CountTokens != in.CountTokens || !out.CheckedAt.Equal(in.CheckedAt) {
		t.Errorf("round trip mismatch: got %+v, want %+v", out, in)
	}
}

func TestLoadMissingIsUnknown(t *testing.T) {
	if out := Load(t.TempDir()); out.CountTokens != Unknown {
		t.Errorf("missing state = %q, want unknown", out.CountTokens)
	}
}
