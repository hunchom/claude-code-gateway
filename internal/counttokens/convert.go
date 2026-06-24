package counttokens

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"  // register decoders for image.DecodeConfig
	_ "image/jpeg" // register decoders for image.DecodeConfig
	_ "image/png"  // register decoders for image.DecodeConfig
)

// ===========================================================================
// Anthropic count_tokens request shape (what Claude Code sends)
// ===========================================================================

type anthropicCountRequest struct {
	Model    string             `json:"model"`
	System   json.RawMessage    `json:"system,omitempty"`
	Messages []anthropicMessage `json:"messages"`
	Tools    []anthropicTool    `json:"tools,omitempty"`
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type contentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	Source   *blockSource    `json:"source,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
	Content  json.RawMessage `json:"content,omitempty"`
}

type blockSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data,omitempty"`
}

// ===========================================================================
// ai-tokenizer SDK shape (what the sidecar's count() expects)
// ===========================================================================

type sdkRequest struct {
	Model       string       `json:"model"`
	Messages    []sdkMessage `json:"messages"`
	Tools       []sdkTool    `json:"tools,omitempty"`
	ExtraTokens int          `json:"extraTokens"`
}

type sdkMessage struct {
	Role    string    `json:"role"`
	Content []sdkPart `json:"content"`
}

type sdkPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ToolName string          `json:"toolName,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
	Output   string          `json:"output,omitempty"`
}

type sdkTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// convertToSDK maps an Anthropic count_tokens request into the ai-tokenizer SDK
// request shape, accumulating flat token estimates for non-text blocks (images,
// PDFs) that the text tokenizer cannot see.
func convertToSDK(req *anthropicCountRequest, cfg *CountConfig) (*sdkRequest, error) {
	// Carry the request's model so the sidecar can pick the matching tokenizer;
	// it falls back to the configured default when the model is empty/unknown.
	out := &sdkRequest{Model: req.Model}
	if len(req.System) > 0 {
		parts, extra, err := normalizeContent(req.System, cfg)
		if err != nil {
			return nil, fmt.Errorf("system: %w", err)
		}
		out.ExtraTokens += extra
		if len(parts) > 0 {
			out.Messages = append(out.Messages, sdkMessage{Role: "system", Content: parts})
		}
	}
	for i, m := range req.Messages {
		parts, extra, err := normalizeContent(m.Content, cfg)
		if err != nil {
			return nil, fmt.Errorf("messages[%d]: %w", i, err)
		}
		out.ExtraTokens += extra
		role := m.Role
		if role != "user" && role != "assistant" && role != "system" {
			role = "user"
		}
		out.Messages = append(out.Messages, sdkMessage{Role: role, Content: parts})
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, sdkTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	return out, nil
}

func normalizeContent(raw json.RawMessage, cfg *CountConfig) ([]sdkPart, int, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, 0, nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, 0, err
		}
		if s == "" {
			return nil, 0, nil
		}
		return []sdkPart{{Type: "text", Text: s}}, 0, nil
	}
	if raw[0] == '[' {
		var blocks []contentBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, 0, err
		}
		var parts []sdkPart
		extra := 0
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if b.Text != "" {
					parts = append(parts, sdkPart{Type: "text", Text: b.Text})
				}
			case "thinking":
				if b.Thinking != "" {
					parts = append(parts, sdkPart{Type: "text", Text: b.Thinking})
				}
			case "tool_use":
				parts = append(parts, sdkPart{Type: "tool-call", ToolName: b.Name, Input: b.Input})
			case "tool_result":
				txt, err := flattenToolResult(b.Content)
				if err != nil {
					return nil, 0, err
				}
				parts = append(parts, sdkPart{Type: "tool-result", Output: txt})
			case "image":
				extra += imageTokens(b.Source, cfg)
			case "document":
				if b.Source != nil && b.Source.MediaType == "application/pdf" {
					extra += cfg.PDFTok
				} else {
					extra += cfg.ImageTok
				}
			default:
				if len(b.Input) > 0 {
					parts = append(parts, sdkPart{Type: "text", Text: string(b.Input)})
				} else if b.Text != "" {
					parts = append(parts, sdkPart{Type: "text", Text: b.Text})
				}
			}
		}
		return parts, extra, nil
	}
	return []sdkPart{{Type: "text", Text: string(raw)}}, 0, nil
}

// imageTokens estimates tokens for an image block. With decodable base64 image
// data it uses Anthropic's documented approximation, width*height/750; otherwise
// (URL sources, unknown formats, decode errors) it falls back to the configured
// flat estimate.
func imageTokens(src *blockSource, cfg *CountConfig) int {
	if src == nil || src.Data == "" {
		return cfg.ImageTok
	}
	raw, err := base64.StdEncoding.DecodeString(src.Data)
	if err != nil {
		return cfg.ImageTok
	}
	ic, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || ic.Width == 0 || ic.Height == 0 {
		return cfg.ImageTok
	}
	return max((ic.Width*ic.Height+749)/750, 1)
}

func flattenToolResult(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		return s, nil
	}
	if raw[0] == '[' {
		var blocks []contentBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return string(raw), nil
		}
		var buf bytes.Buffer
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				if buf.Len() > 0 {
					buf.WriteByte('\n')
				}
				buf.WriteString(b.Text)
			}
		}
		if buf.Len() == 0 {
			return string(raw), nil
		}
		return buf.String(), nil
	}
	return string(raw), nil
}
