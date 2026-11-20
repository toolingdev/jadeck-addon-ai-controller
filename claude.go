package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// Anthropic (Claude) through the official SDK. Default model Claude Opus
// 5.5. Thinking stays adaptive (the model default); the assistant's
// replies are short, so effort is left at the model's default.

const defaultClaude = "claude-opus-5-5"

// Models that take server-side refusal fallbacks ("default" routing).
var fallbackModels = map[string]bool{
	"claude-fable-5-2": true, "claude-fable-5-1": true, "claude-opus-5-5": true, "claude-opus-5": true, "claude-sonnet-5-5": true,
}

type claude struct {
	client anthropic.Client
	model  string
	msgs   []anthropic.BetaMessageParam
}

func newClaude(cfg config) (*claude, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("enter your Anthropic API key (console.anthropic.com)")
	}
	m := cfg.Model
	if m == "" {
		m = defaultClaude
	}
	return &claude{client: anthropic.NewClient(option.WithAPIKey(cfg.APIKey)), model: m}, nil
}

func (c *claude) name() string { return "Claude (" + c.model + ")" }

func (c *claude) addUser(text string) {
	c.msgs = append(c.msgs, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(text)))
}

func (c *claude) addResults(rs []result) {
	blocks := make([]anthropic.BetaContentBlockParamUnion, 0, len(rs))
	for _, r := range rs {
		blocks = append(blocks, anthropic.NewBetaToolResultBlock(r.ID, r.Text, r.IsError))
	}
	c.msgs = append(c.msgs, anthropic.NewBetaUserMessage(blocks...))
}

func (c *claude) next(ctx context.Context, system string, tools []tool) (string, []call, error) {
	ts := make([]anthropic.BetaToolUnionParam, 0, len(tools))
	for _, t := range tools {
		props, _ := t.Schema["properties"].(map[string]any)
		var req []string
		if r, ok := t.Schema["required"].([]any); ok {
			for _, x := range r {
				if s, ok := x.(string); ok {
					req = append(req, s)
				}
			}
		}
		tp := anthropic.BetaToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.BetaToolInputSchemaParam{Properties: props, Required: req},
		}
		ts = append(ts, anthropic.BetaToolUnionParam{OfTool: &tp})
	}
	params := anthropic.BetaMessageNewParams{
		Model:     c.model,
		MaxTokens: 16000,
		System:    []anthropic.BetaTextBlockParam{{Text: system}},
		Messages:  c.msgs,
		Tools:     ts,
	}
	if fallbackModels[c.model] {
		// A policy decline is re-served by a fallback model in the same call.
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
	}
	resp, err := c.client.Beta.Messages.New(ctx, params)
	if err != nil {
		return "", nil, fmt.Errorf("Claude: %w", err)
	}
	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		return "I can't help with that one.", nil, nil
	}
	// Keep the whole response (thinking blocks included) for the next call.
	c.msgs = append(c.msgs, resp.ToParam())
	var text string
	var calls []call
	for _, b := range resp.Content {
		switch v := b.AsAny().(type) {
		case anthropic.BetaTextBlock:
			text += v.Text
		case anthropic.BetaToolUseBlock:
			var args map[string]any
			if err := json.Unmarshal([]byte(v.JSON.Input.Raw()), &args); err != nil {
				args = map[string]any{}
			}
			calls = append(calls, call{ID: v.ID, Name: v.Name, Args: args})
		}
	}
	if resp.StopReason == anthropic.BetaStopReasonMaxTokens && len(calls) == 0 {
		text += " (cut short)"
	}
	return text, calls, nil
}
