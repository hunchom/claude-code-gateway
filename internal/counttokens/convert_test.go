package counttokens

import (
	"encoding/json"
	"testing"
)

func testCfg() *CountConfig {
	return &CountConfig{Model: "anthropic/claude-sonnet-4.5", ImageTok: 1600, PDFTok: 3000}
}

func TestIsCountTokensPath(t *testing.T) {
	good := []string{
		"/v1/messages/count_tokens",
		"/anthropic/v1/messages/count_tokens",
		"/messages/count_tokens",
		"/some/prefix/v1/messages/count_tokens",
	}
	for _, p := range good {
		if !IsCountTokensPath(p) {
			t.Errorf("IsCountTokensPath(%q) = false, want true", p)
		}
	}
	bad := []string{"/v1/messages", "/v1/messages/count", "/", "/count_tokens"}
	for _, p := range bad {
		if IsCountTokensPath(p) {
			t.Errorf("IsCountTokensPath(%q) = true, want false", p)
		}
	}
}

func TestConvertStringContent(t *testing.T) {
	req := &anthropicCountRequest{
		System:   json.RawMessage(`"you are helpful"`),
		Messages: []anthropicMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
	}
	out, err := convertToSDK(req, testCfg())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(out.Messages))
	}
	if out.Messages[0].Role != "system" {
		t.Errorf("first role = %q, want system", out.Messages[0].Role)
	}
	if out.Messages[1].Content[0].Text != "hello" {
		t.Errorf("user text = %q", out.Messages[1].Content[0].Text)
	}
}

func TestConvertBlocks(t *testing.T) {
	content := `[
		{"type":"text","text":"hi"},
		{"type":"thinking","thinking":"hmm"},
		{"type":"tool_use","name":"calc","input":{"x":1}},
		{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}},
		{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"AAAA"}}
	]`
	req := &anthropicCountRequest{Messages: []anthropicMessage{{Role: "user", Content: json.RawMessage(content)}}}
	out, err := convertToSDK(req, testCfg())
	if err != nil {
		t.Fatal(err)
	}
	if out.ExtraTokens != 4600 {
		t.Errorf("extra tokens = %d, want 4600 (1600 image + 3000 pdf)", out.ExtraTokens)
	}
	parts := out.Messages[0].Content
	if len(parts) != 3 {
		t.Fatalf("parts = %d, want 3 (text, thinking, tool-call)", len(parts))
	}
	if parts[2].Type != "tool-call" || parts[2].ToolName != "calc" {
		t.Errorf("tool-call part = %+v", parts[2])
	}
}

func TestConvertToolResult(t *testing.T) {
	content := `[{"type":"tool_result","content":[{"type":"text","text":"line1"},{"type":"text","text":"line2"}]}]`
	req := &anthropicCountRequest{Messages: []anthropicMessage{{Role: "user", Content: json.RawMessage(content)}}}
	out, err := convertToSDK(req, testCfg())
	if err != nil {
		t.Fatal(err)
	}
	got := out.Messages[0].Content[0]
	if got.Type != "tool-result" || got.Output != "line1\nline2" {
		t.Errorf("tool-result part = %+v", got)
	}
}

func TestConvertRoleNormalization(t *testing.T) {
	req := &anthropicCountRequest{Messages: []anthropicMessage{{Role: "tool", Content: json.RawMessage(`"x"`)}}}
	out, err := convertToSDK(req, testCfg())
	if err != nil {
		t.Fatal(err)
	}
	if out.Messages[0].Role != "user" {
		t.Errorf("role = %q, want user (unknown roles fold to user)", out.Messages[0].Role)
	}
}

func TestFlattenToolResult(t *testing.T) {
	cases := map[string]json.RawMessage{
		"plain":  json.RawMessage(`"hi"`),
		"blocks": json.RawMessage(`[{"type":"text","text":"a"},{"type":"text","text":"b"}]`),
	}
	want := map[string]string{"plain": "hi", "blocks": "a\nb"}
	for name, raw := range cases {
		got, err := flattenToolResult(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want[name] {
			t.Errorf("%s: got %q, want %q", name, got, want[name])
		}
	}
}
