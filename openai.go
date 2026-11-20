package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAI-style chat completions with function tools: OpenAI, Google
// Gemini (its OpenAI-compatible endpoint), Ollama and other services
// that speak the same API.

var openaiBase = map[string]string{
	"openai": "https://api.openai.com/v1",
	"gemini": "https://generativelanguage.googleapis.com/v1beta/openai",
}

type oaMsg struct {
	Role       string       `json:"role"`
	Content    string       `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openaiCompat struct {
	base, key, model, label string
	http                    *http.Client
	msgs                    []oaMsg
}

func newOpenAI(cfg config) (*openaiCompat, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = openaiBase[cfg.Provider]
	}
	if base == "" && cfg.Provider == "ollama" {
		base = "http://127.0.0.1:11434/v1"
	}
	if base == "" {
		return nil, fmt.Errorf("enter the service address (…/v1)")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("enter the model name for this service")
	}
	if cfg.APIKey == "" && cfg.Provider != "ollama" && cfg.BaseURL == "" {
		return nil, fmt.Errorf("enter your API key for this service")
	}
	return &openaiCompat{base: base, key: cfg.APIKey, model: cfg.Model, label: cfg.Provider,
		http: &http.Client{Timeout: 2 * time.Minute}}, nil
}

func (o *openaiCompat) name() string { return o.label + " (" + o.model + ")" }

func (o *openaiCompat) addUser(text string) {
	o.msgs = append(o.msgs, oaMsg{Role: "user", Content: text})
}

func (o *openaiCompat) addResults(rs []result) {
	for _, r := range rs {
		txt := r.Text
		if r.IsError {
			txt = "ERROR: " + txt
		}
		o.msgs = append(o.msgs, oaMsg{Role: "tool", ToolCallID: r.ID, Content: txt})
	}
}

func (o *openaiCompat) next(ctx context.Context, system string, tools []tool) (string, []call, error) {
	ts := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		ts = append(ts, map[string]any{"type": "function", "function": map[string]any{
			"name": t.Name, "description": t.Description, "parameters": t.Schema}})
	}
	body := map[string]any{"model": o.model, "messages": append([]oaMsg{{Role: "system", Content: system}}, o.msgs...)}
	if len(ts) > 0 {
		body["tools"] = ts
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", o.base+"/chat/completions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if o.key != "" {
		req.Header.Set("Authorization", "Bearer "+o.key)
	}
	resp, err := o.http.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", o.label, err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return "", nil, fmt.Errorf("%s answered %d: %s", o.label, resp.StatusCode, firstLine(string(rb)))
	}
	var out struct {
		Choices []struct {
			Message oaMsg `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rb, &out); err != nil || len(out.Choices) == 0 {
		return "", nil, fmt.Errorf("%s: unreadable answer", o.label)
	}
	m := out.Choices[0].Message
	m.Role = "assistant"
	o.msgs = append(o.msgs, m)
	var calls []call
	for _, tc := range m.ToolCalls {
		var args map[string]any
		json.Unmarshal([]byte(tc.Function.Arguments), &args)
		if args == nil {
			args = map[string]any{}
		}
		calls = append(calls, call{ID: tc.ID, Name: tc.Function.Name, Args: args})
	}
	return m.Content, calls, nil
}

func providerFor(cfg config) (func() (provider, error), error) {
	switch cfg.Provider {
	case "", "anthropic":
		if _, err := newClaude(cfg); err != nil {
			return nil, err
		}
		return func() (provider, error) { return newClaude(cfg) }, nil
	case "openai", "gemini", "ollama", "openai-compatible":
		if _, err := newOpenAI(cfg); err != nil {
			return nil, err
		}
		return func() (provider, error) { return newOpenAI(cfg) }, nil
	}
	return nil, fmt.Errorf("unknown AI service %q", cfg.Provider)
}
