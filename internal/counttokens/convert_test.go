package counttokens

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
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
	// Other real Claude Code endpoints must pass through, not be intercepted.
	bad := []string{
		"/v1/messages",
		"/v1/messages/count",
		"/v1/messages/batches",
		"/v1/messages/batches/msgbatch_123",
		"/",
		"/count_tokens",
	}
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

func TestImageTokensFromDimensions(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 30, 30))); err != nil {
		t.Fatal(err)
	}
	data := base64.StdEncoding.EncodeToString(buf.Bytes())
	content := `[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + data + `"}}]`
	req := &anthropicCountRequest{Messages: []anthropicMessage{{Role: "user", Content: json.RawMessage(content)}}}
	out, err := convertToSDK(req, testCfg())
	if err != nil {
		t.Fatal(err)
	}
	want := (30*30 + 749) / 750 // dimension-based estimate, not the flat fallback
	if out.ExtraTokens != want {
		t.Errorf("image extra tokens = %d, want %d (width*height/750)", out.ExtraTokens, want)
	}
}

func TestImageTokensFallback(t *testing.T) {
	// Non-decodable data falls back to the configured flat estimate.
	content := `[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]`
	req := &anthropicCountRequest{Messages: []anthropicMessage{{Role: "user", Content: json.RawMessage(content)}}}
	out, err := convertToSDK(req, testCfg())
	if err != nil {
		t.Fatal(err)
	}
	if out.ExtraTokens != testCfg().ImageTok {
		t.Errorf("fallback image tokens = %d, want %d", out.ExtraTokens, testCfg().ImageTok)
	}
}

func TestModelMapOverride(t *testing.T) {
	cfg := testCfg()
	cfg.ModelMap = map[string]string{"my-litellm-alias": "anthropic/claude-sonnet-4.5"}
	req := &anthropicCountRequest{
		Model:    "my-litellm-alias",
		Messages: []anthropicMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	}
	out, err := convertToSDK(req, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if out.Model != "anthropic/claude-sonnet-4.5" {
		t.Errorf("model = %q, want mapped ai-tokenizer key", out.Model)
	}
}

func TestModelMapPassthrough(t *testing.T) {
	req := &anthropicCountRequest{
		Model:    "claude-x",
		Messages: []anthropicMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	}
	out, err := convertToSDK(req, testCfg())
	if err != nil {
		t.Fatal(err)
	}
	if out.Model != "claude-x" {
		t.Errorf("model = %q, want unchanged when no map entry", out.Model)
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

// FuzzConvertToSDK feeds arbitrary (but JSON-parseable) count_tokens request
// bodies through the conversion to ensure it never panics on malformed input.
func FuzzConvertToSDK(f *testing.F) {
	f.Add([]byte(`{"model":"claude-x","messages":[{"role":"user","content":"hi"}]}`))
	f.Add([]byte(`{"system":"s","messages":[{"role":"user","content":[{"type":"text","text":"x"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}},{"type":"tool_use","name":"t","input":{"a":1}}]}]}`))
	f.Add([]byte(`{"messages":[{"role":"weird","content":[{"type":"tool_result","content":[{"type":"text","text":"r"}]}]}]}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var req anthropicCountRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return // only exercise inputs that parse as the request shape
		}
		// Must not panic regardless of content.
		_, _ = convertToSDK(&req, testCfg())
	})
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
