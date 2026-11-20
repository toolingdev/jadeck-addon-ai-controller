package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/toolingdev/ptz-controller/sdk/addon"
)

// A conversation per screen ("session"). One turn: the person's message,
// then model calls until it answers in words. Tool calls that only read
// run at once; ones that change something wait for "Do it" when Ask
// before acting is on.

type config struct {
	Provider     string   `json:"provider"`
	Model        string   `json:"model"`
	APIKey       string   `json:"apiKey"`
	BaseURL      string   `json:"baseUrl"`
	Permissions  []string `json:"permissions"`
	AskFirst     *bool    `json:"askFirst"`
	Instructions string   `json:"instructions"`
}

// call is one tool call the model asked for.
type call struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Args  map[string]any `json:"args"`
	Label string         `json:"label"`
}

type result struct {
	ID      string
	Name    string
	Text    string
	IsError bool
}

// provider talks to one AI service; it owns its native history.
type provider interface {
	name() string
	addUser(text string)
	// next makes one model call: its words, and any tool calls.
	next(ctx context.Context, system string, tools []tool) (text string, calls []call, err error)
	addResults(rs []result)
}

type line struct {
	Role    string `json:"role"` // user | assistant | action | error
	Text    string `json:"text"`
	Pending []call `json:"pending,omitempty"`
	At      string `json:"at"`
}

type session struct {
	p       provider
	pending []call
	lines   []line
	last    time.Time
}

// ctlAPI is the controller as the assistant uses it (a fake in tests).
type ctlAPI interface {
	Tools(ctx context.Context) ([]tool, error)
	Call(ctx context.Context, name string, args map[string]any) (string, bool)
	reset()
}

type conn struct {
	cfg    config
	ctl    ctlAPI
	access *addon.ControllerAccess
	mk     func() (provider, error)
	mu     sync.Mutex
	sess   map[string]*session
}

func open(raw json.RawMessage) (*conn, error) {
	var cfg config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	acc := addon.ControllerFrom(raw)
	c := &conn{cfg: cfg, ctl: &controller{access: acc}, access: acc, sess: map[string]*session{}}
	mk, err := providerFor(cfg)
	if err != nil {
		return nil, err
	}
	c.mk = mk
	return c, nil
}

func (c *conn) askFirst() bool { return c.cfg.AskFirst == nil || *c.cfg.AskFirst }

func (c *conn) Close() error {
	c.ctl.reset()
	return nil
}

func (c *conn) system() string {
	now := time.Now()
	s := "You run a venue AV booth through tools: cameras, a video switcher, slides, an audio mixer, macros, stage messages and scheduled actions. " +
		"The person talking is a volunteer at the booth during a live service. Keep replies short (one or two sentences) and plain. " +
		"Read status with the read-only tools when you need to know names (presets, inputs, channels) instead of guessing. " +
		"For anything later (\"in 5 minutes\", \"at 10:35\"), use schedule_add with in_seconds or at; it shows on screens under Coming up. " +
		"After acting, say what happened from the tool results; never claim something happened if a tool failed. " +
		"If you can't do something with the tools you have, say so.\n" +
		fmt.Sprintf("Now: %s (%s).", now.Format(time.RFC3339), now.Format("Monday 3:04 PM"))
	if strings.TrimSpace(c.cfg.Instructions) != "" {
		s += "\nThe booth's own notes: " + c.cfg.Instructions
	}
	return s
}

func (c *conn) get(id string) (*session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id == "" {
		id = "default"
	}
	// forget conversations idle for a day
	for k, s := range c.sess {
		if time.Since(s.last) > 24*time.Hour {
			delete(c.sess, k)
		}
	}
	s := c.sess[id]
	if s == nil {
		p, err := c.mk()
		if err != nil {
			return nil, err
		}
		s = &session{p: p}
		c.sess[id] = s
	}
	s.last = time.Now()
	return s, nil
}

func label(t tool, args map[string]any) string {
	name := t.Title
	if name == "" {
		name = t.Name
	}
	if len(args) == 0 {
		return name
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		var v string
		switch x := args[k].(type) {
		case []any:
			n := fmt.Sprintf("%d %s", len(x), k)
			if len(x) == 1 {
				n = strings.TrimSuffix(n, "s")
			}
			parts = append(parts, n)
			continue
		default:
			b, _ := json.Marshal(x)
			v = string(b)
		}
		if len(v) > 80 {
			v = v[:80] + "…"
		}
		parts = append(parts, k+" "+strings.Trim(v, `"`))
	}
	return name + " · " + strings.Join(parts, ", ")
}

// run continues the conversation until the model answers in words or
// asks to change something that needs approval.
func (c *conn) run(ctx context.Context, s *session) error {
	tools, err := c.ctl.Tools(ctx)
	if err != nil {
		return err
	}
	byName := map[string]tool{}
	for _, t := range tools {
		byName[t.Name] = t
	}
	for step := 0; step < 10; step++ {
		text, calls, err := s.p.next(ctx, c.system(), tools)
		if err != nil {
			return err
		}
		if strings.TrimSpace(text) != "" {
			s.lines = append(s.lines, line{Role: "assistant", Text: strings.TrimSpace(text), At: time.Now().Format(time.RFC3339)})
		}
		if len(calls) == 0 {
			return nil
		}
		needs := false
		for i := range calls {
			t, ok := byName[calls[i].Name]
			calls[i].Label = label(t, calls[i].Args)
			if !ok || !t.ReadOnly {
				needs = true
			}
		}
		if needs && c.askFirst() {
			s.pending = calls
			s.lines = append(s.lines, line{Role: "assistant", Text: "I'll do this:", Pending: calls, At: time.Now().Format(time.RFC3339)})
			return nil
		}
		s.p.addResults(c.execute(ctx, s, calls, byName))
	}
	s.lines = append(s.lines, line{Role: "error", Text: "Stopped after 10 steps — ask again more simply?"})
	return nil
}

func (c *conn) execute(ctx context.Context, s *session, calls []call, byName map[string]tool) []result {
	rs := make([]result, 0, len(calls))
	for _, cl := range calls {
		text, isErr := c.ctl.Call(ctx, cl.Name, cl.Args)
		rs = append(rs, result{ID: cl.ID, Name: cl.Name, Text: text, IsError: isErr})
		if t := byName[cl.Name]; !t.ReadOnly {
			mark := "✓ "
			if isErr {
				mark = "✗ "
			}
			s.lines = append(s.lines, line{Role: "action", Text: mark + cl.Label + map[bool]string{true: " — " + firstLine(text), false: ""}[isErr],
				At: time.Now().Format(time.RFC3339)})
		}
	}
	return rs
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

func (c *conn) reply(s *session, from int) map[string]any {
	return map[string]any{"lines": s.lines[from:], "pending": len(s.pending) > 0}
}

// AddonAction: chat, approve, reject, reset.
func (c *conn) AddonAction(ctx context.Context, name string, params json.RawMessage) (any, error) {
	var p struct {
		Session string `json:"session"`
		Message string `json:"message"`
	}
	if len(params) > 0 {
		json.Unmarshal(params, &p)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	s, err := c.get(p.Session)
	if err != nil {
		return nil, err
	}
	from := len(s.lines)
	fail := func(err error) (any, error) {
		s.lines = append(s.lines, line{Role: "error", Text: err.Error(), At: time.Now().Format(time.RFC3339)})
		return c.reply(s, from), nil
	}
	switch name {
	case "chat":
		msg := strings.TrimSpace(p.Message)
		if msg == "" {
			return nil, fmt.Errorf("say something first")
		}
		if len(s.pending) > 0 {
			// a new message instead of Do it / Cancel declines the waiting actions
			s.p.addResults(declined(s.pending))
			s.pending = nil
		}
		s.lines = append(s.lines, line{Role: "user", Text: msg, At: time.Now().Format(time.RFC3339)})
		s.p.addUser(msg)
		if err := c.run(ctx, s); err != nil {
			return fail(err)
		}
	case "approve":
		if len(s.pending) == 0 {
			return nil, fmt.Errorf("nothing is waiting")
		}
		tools, _ := c.ctl.Tools(ctx)
		byName := map[string]tool{}
		for _, t := range tools {
			byName[t.Name] = t
		}
		calls := s.pending
		s.pending = nil
		s.p.addResults(c.execute(ctx, s, calls, byName))
		if err := c.run(ctx, s); err != nil {
			return fail(err)
		}
	case "reject":
		if len(s.pending) == 0 {
			return nil, fmt.Errorf("nothing is waiting")
		}
		s.p.addResults(declined(s.pending))
		s.pending = nil
		s.lines = append(s.lines, line{Role: "action", Text: "– Cancelled, nothing was done", At: time.Now().Format(time.RFC3339)})
	case "test":
		tools, err := c.ctl.Tools(ctx)
		if err != nil {
			return nil, fmt.Errorf("controller: %w", err)
		}
		p, err := c.mk()
		if err != nil {
			return nil, err
		}
		p.addUser("Reply with just: OK")
		if _, _, err := p.next(ctx, "You are being tested. Reply with just OK.", nil); err != nil {
			return nil, err
		}
		return map[string]any{"detail": fmt.Sprintf("%s answered · can use %d controller tools", p.name(), len(tools))}, nil
	case "reset":
		c.mu.Lock()
		delete(c.sess, p.Session)
		c.mu.Unlock()
		return map[string]any{"lines": []line{}, "pending": false}, nil
	default:
		return nil, fmt.Errorf("%w %q", addon.ErrUnknownAction, name)
	}
	return c.reply(s, from), nil
}

func declined(calls []call) []result {
	rs := make([]result, 0, len(calls))
	for _, cl := range calls {
		rs = append(rs, result{ID: cl.ID, Name: cl.Name, Text: "The person at the booth declined this; it was not done.", IsError: true})
	}
	return rs
}

// AddonState: which service, whether the controller is reachable, and
// each screen's conversation (so a reopened widget shows it again).
func (c *conn) AddonState(ctx context.Context) (map[string]any, error) {
	st := map[string]any{"provider": c.cfg.Provider, "askFirst": c.askFirst()}
	if c.access != nil {
		st["can"] = c.access.Can
	}
	c.mu.Lock()
	convs := map[string]any{}
	for id, s := range c.sess {
		convs[id] = map[string]any{"lines": s.lines, "pending": len(s.pending) > 0}
	}
	c.mu.Unlock()
	st["sessions"] = convs
	return st, nil
}
