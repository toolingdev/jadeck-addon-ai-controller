// AI controller: a jadeck add-on. People talk to the booth in
// plain words ("go to the pulpit shot, and in 5 minutes cut to camera
// 2") and an AI service of their choice runs it through the controller's
// own agent tools, asking first unless told not to.
package main

import (
	"encoding/json"

	"github.com/toolingdev/jadeck/sdk/addon"
)

// Version is set by the release build.
var Version = "1.0.2"

func main() { addon.Serve(spec()) }

var sampleChat = map[string]any{
	"messages": []map[string]any{
		{"role": "user", "text": "Go to the pulpit shot, and in 5 minutes cut to camera 2"},
		{"role": "assistant", "text": "I'll do two things:", "pending": []map[string]any{
			{"label": "Camera: recall preset “Pulpit”"},
			{"label": "Schedule in 5 min: switcher preview 2, then cut"},
		}},
	},
}

func spec() addon.Spec {
	return addon.Spec{
		Manifest: addon.Manifest{
			ID: "ai-controller", Name: "AI controller", Version: Version, Publisher: "jadeck",
			APIVersion: addon.APIVersion, Repo: "toolingdev/jadeck-addon-ai-controller", Controller: true,
			Description: "Talk to the booth: “cut to camera 2 in 5 minutes”. Your AI service, your key.",
			Reaches:     "The AI service you pick (with your key), and this controller, only with the permissions you tick",
			ConnectionTypes: []addon.ConnectionType{{
				ID: "assistant", Label: "AI controller", Capabilities: []string{addon.CapAssistant},
				Description: "What you type, and the booth's status the assistant reads, go to the AI service you pick; it bills you for it. Ollama keeps everything on your network.",
				Fields: []addon.Field{
					{Key: "provider", Label: "AI service", Type: "select", Default: "anthropic", Required: true, Options: []addon.Option{
						{Value: "anthropic", Label: "Anthropic (Claude)"}, {Value: "openai", Label: "OpenAI"},
						{Value: "gemini", Label: "Google Gemini"}, {Value: "ollama", Label: "Ollama (on your network)"},
						{Value: "openai-compatible", Label: "Other (OpenAI-compatible)"}}},
					{Key: "model", Label: "Model", Type: "text", Placeholder: "claude-opus-5-5",
						Help: "Blank = Claude Opus 5.5 for Anthropic. For the others, the model name from that service (e.g. one listed in its console, or `ollama list`)."},
					{Key: "apiKey", Label: "API key", Type: "password", Help: "From the AI service's console. Not needed for Ollama."},
					{Key: "baseUrl", Label: "Service address", Type: "text", Placeholder: "http://192.168.1.50:11434/v1",
						Help: "Ollama or another OpenAI-compatible service. Leave blank for Anthropic, OpenAI and Gemini."},
					{Key: "permissions", Label: "It may", Type: "scopes",
						Default: []string{"camera.read", "camera.move", "switcher", "slides", "macros.run", "stage", "schedule"},
						Help:    "What the assistant may touch. Audio mutes are off unless you tick them."},
					{Key: "askFirst", Label: "Ask before acting", Type: "bool", Default: true,
						Help: "On: it shows what it will do and waits for Do it. Reading status never asks."},
					{Key: "instructions", Label: "Extra instructions", Type: "textarea", Advanced: true,
						Placeholder: "e.g. Camera 1 is the wide shot. Never cut during prayer."},
				},
				Sample:     map[string]any{"provider": "anthropic", "model": "claude-opus-5-5", "apiKey": "••••••••", "askFirst": true},
				SampleTest: "Claude answered · can use 24 controller tools",
			}},
			Actions: []addon.ActionDef{
				{ID: "chat", Label: "Say something", Params: []addon.Field{{Key: "message", Label: "Message", Type: "text"}, {Key: "session", Label: "Session", Type: "text"}}},
				{ID: "approve", Label: "Do it", Params: []addon.Field{{Key: "session", Label: "Session", Type: "text"}}},
				{ID: "reject", Label: "Cancel", Params: []addon.Field{{Key: "session", Label: "Session", Type: "text"}}},
				{ID: "test", Label: "Test"},
				{ID: "reset", Label: "New conversation", Params: []addon.Field{{Key: "session", Label: "Session", Type: "text"}}},
			},
			Widgets: []addon.WidgetDef{{Kind: "chat", Type: "assistant", Label: "Assistant",
				Description: "Tell the booth what to do; it shows each action before doing it.", Sample: sampleChat}},
			Changelog: "First release.",
		},
		Open: map[string]addon.OpenFunc{"assistant": func(raw json.RawMessage) (any, error) { return open(raw) }},
	}
}
