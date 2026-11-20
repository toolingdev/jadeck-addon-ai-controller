package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/toolingdev/jadeck/sdk/addon/addontest"
)

// fakeCtl is the controller: one read tool, one tool that changes things.
type fakeCtl struct{ done []string }

func (f *fakeCtl) Tools(context.Context) ([]tool, error) {
	return []tool{
		{Name: "presets_list", Title: "List presets", ReadOnly: true, Schema: map[string]any{"type": "object"}},
		{Name: "preset_recall", Title: "Recall preset", Schema: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}},
	}, nil
}
func (f *fakeCtl) Call(_ context.Context, name string, args map[string]any) (string, bool) {
	f.done = append(f.done, name)
	if name == "presets_list" {
		return `["Main","Pulpit"]`, false
	}
	return "recalled " + args["name"].(string), false
}
func (f *fakeCtl) reset() {}

// script is a fake AI service: each turn returns the next step.
type script struct {
	steps   [][]call
	words   []string
	n       int
	results []result
}

func (s *script) name() string           { return "script" }
func (s *script) addUser(string)         {}
func (s *script) addResults(rs []result) { s.results = append(s.results, rs...) }
func (s *script) next(context.Context, string, []tool) (string, []call, error) {
	i := s.n
	s.n++
	if i >= len(s.steps) {
		return "Done.", nil, nil
	}
	return s.words[i], s.steps[i], nil
}

func newTest(ask bool, sc *script) (*conn, *fakeCtl) {
	f := &fakeCtl{}
	c := &conn{cfg: config{AskFirst: &ask}, ctl: f, sess: map[string]*session{}}
	c.mk = func() (provider, error) { return sc, nil }
	return c, f
}

func act(t *testing.T, c *conn, name string, p map[string]any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(p)
	out, err := c.AddonAction(context.Background(), name, b)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out.(map[string]any)
}

func TestAsksBeforeActing(t *testing.T) {
	sc := &script{
		steps: [][]call{{{ID: "1", Name: "presets_list"}}, {{ID: "2", Name: "preset_recall", Args: map[string]any{"name": "Pulpit"}}}},
		words: []string{"", "Going to the pulpit shot."},
	}
	c, f := newTest(true, sc)
	r := act(t, c, "chat", map[string]any{"session": "ipad", "message": "pulpit shot"})
	if r["pending"] != true || len(f.done) != 1 || f.done[0] != "presets_list" {
		t.Fatalf("read tool should run, change should wait: %v %v", r, f.done)
	}
	lines := r["lines"].([]line)
	if !strings.Contains(lines[len(lines)-1].Pending[0].Label, "Recall preset · name Pulpit") {
		t.Fatalf("label %+v", lines)
	}
	r = act(t, c, "approve", map[string]any{"session": "ipad"})
	if r["pending"] != false || f.done[1] != "preset_recall" {
		t.Fatalf("after approve: %v %v", r, f.done)
	}
	// Cancel tells the model nothing happened.
	sc2 := &script{steps: [][]call{{{ID: "3", Name: "preset_recall", Args: map[string]any{"name": "Main"}}}}, words: []string{""}}
	c2, f2 := newTest(true, sc2)
	act(t, c2, "chat", map[string]any{"message": "main"})
	act(t, c2, "reject", nil)
	if len(f2.done) != 0 || !sc2.results[0].IsError || !strings.Contains(sc2.results[0].Text, "declined") {
		t.Fatalf("reject: %v %+v", f2.done, sc2.results)
	}
}

func TestActsRightAwayWhenAskFirstOff(t *testing.T) {
	sc := &script{steps: [][]call{{{ID: "1", Name: "preset_recall", Args: map[string]any{"name": "Main"}}}}, words: []string{""}}
	c, f := newTest(false, sc)
	r := act(t, c, "chat", map[string]any{"message": "main"})
	if r["pending"] != false || len(f.done) != 1 {
		t.Fatalf("%v %v", r, f.done)
	}
}

func TestOpenAICompatible(t *testing.T) {
	var got map[string]any
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(401)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		n++
		if n == 1 {
			w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"presets_list","arguments":"{}"}}]}}]}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"You have Main and Pulpit."}}]}`))
	}))
	defer srv.Close()
	ask := true
	c := &conn{cfg: config{AskFirst: &ask}, ctl: &fakeCtl{}, sess: map[string]*session{}}
	mk, err := providerFor(config{Provider: "openai-compatible", BaseURL: srv.URL + "/v1", APIKey: "k", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	c.mk = mk
	r := act(t, c, "chat", map[string]any{"message": "what presets?"})
	lines := r["lines"].([]line)
	if lines[len(lines)-1].Text != "You have Main and Pulpit." {
		t.Fatalf("%+v", lines)
	}
	msgs := got["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "tool" || last["tool_call_id"] != "c1" {
		t.Fatalf("tool result not sent back: %v", last)
	}
}

func TestClaudeThroughSDK(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		key := r.Header.Get("X-Api-Key")
		if key == "" {
			key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if body["model"] != "claude-opus-5-5" || key != "sk-test" || r.Header.Get("Anthropic-Beta") != "server-side-fallback-2026-07-01" {
			t.Errorf("model %v headers %v", body["model"], r.Header)
		}
		n++
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			w.Write([]byte(`{"id":"m1","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1},
			  "content":[{"type":"tool_use","id":"tu1","name":"presets_list","input":{}}]}`))
			return
		}
		msgs := body["messages"].([]any)
		last := msgs[len(msgs)-1].(map[string]any)["content"].([]any)[0].(map[string]any)
		if last["type"] != "tool_result" || last["tool_use_id"] != "tu1" {
			t.Errorf("tool result not sent back: %v", last)
		}
		w.Write([]byte(`{"id":"m2","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1},
		  "content":[{"type":"text","text":"Main and Pulpit."}]}`))
	}))
	defer srv.Close()
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	ask := true
	c := &conn{cfg: config{AskFirst: &ask}, ctl: &fakeCtl{}, sess: map[string]*session{}}
	mk, err := providerFor(config{Provider: "anthropic", APIKey: "sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	c.mk = mk
	r := act(t, c, "chat", map[string]any{"message": "presets?"})
	lines := r["lines"].([]line)
	if lines[len(lines)-1].Text != "Main and Pulpit." {
		t.Fatalf("%+v", lines)
	}
}

func TestManifest(t *testing.T) {
	addontest.Start(t, spec())
}
