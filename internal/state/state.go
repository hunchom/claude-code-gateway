// Package state persists what the gateway has learned about its upstream so the
// answer survives restarts and is shared across launches.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Capability records whether the upstream supports a probed feature.
type Capability string

const (
	Unknown     Capability = "unknown"
	Supported   Capability = "supported"
	Unsupported Capability = "unsupported"
)

// State is the on-disk record written to <dir>/state.json.
type State struct {
	Endpoint    string     `json:"endpoint"`
	CountTokens Capability `json:"count_tokens"`
	CheckedAt   time.Time  `json:"checked_at"`
}

func file(dir string) string { return filepath.Join(dir, "state.json") }

// Load reads state from dir, returning a zero State (Unknown) when none exists
// or the file is unreadable.
func Load(dir string) State {
	s := State{CountTokens: Unknown}
	if data, err := os.ReadFile(file(dir)); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	if s.CountTokens == "" {
		s.CountTokens = Unknown
	}
	return s
}

// Save writes state to dir atomically (unique temp file + rename). The unique
// temp name makes concurrent saves (e.g. simultaneous capability probes) safe.
func Save(dir string, s State) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "state-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, file(dir)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
