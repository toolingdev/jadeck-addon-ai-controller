package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/toolingdev/ptz-controller/sdk/addon"
)

// controller is the MCP connection to the PTZ controller, with the
// token (and so the permissions) this connection was given.

type tool struct {
	Name, Title, Description string
	Schema                   map[string]any
	ReadOnly                 bool
}

type controller struct {
	access *addon.ControllerAccess

	mu    sync.Mutex
	sess  *mcp.ClientSession
	tools []tool
}

type bearer struct {
	token string
	rt    http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.rt.RoundTrip(r)
}

func (c *controller) session(ctx context.Context) (*mcp.ClientSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess != nil {
		return c.sess, nil
	}
	if c.access == nil {
		return nil, fmt.Errorf("the controller gave this add-on no access (is it a Controller add-on?)")
	}
	cl := mcp.NewClient(&mcp.Implementation{Name: "ptz-ai-controller", Version: Version}, nil)
	tr := &mcp.StreamableClientTransport{
		Endpoint:   strings.TrimRight(c.access.URL, "/") + "/mcp",
		HTTPClient: &http.Client{Timeout: 60 * time.Second, Transport: bearer{c.access.Token, http.DefaultTransport}},
	}
	s, err := cl.Connect(ctx, tr, nil)
	if err != nil {
		return nil, fmt.Errorf("could not reach the controller: %w", err)
	}
	c.sess = s
	return s, nil
}

func (c *controller) reset() {
	c.mu.Lock()
	if c.sess != nil {
		c.sess.Close()
	}
	c.sess, c.tools = nil, nil
	c.mu.Unlock()
}

// Tools lists what this connection may use (cached).
func (c *controller) Tools(ctx context.Context) ([]tool, error) {
	c.mu.Lock()
	if c.tools != nil {
		t := c.tools
		c.mu.Unlock()
		return t, nil
	}
	c.mu.Unlock()
	s, err := c.session(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.ListTools(ctx, nil)
	if err != nil {
		c.reset()
		return nil, err
	}
	var out []tool
	for _, t := range res.Tools {
		schema := map[string]any{"type": "object", "properties": map[string]any{}}
		if t.InputSchema != nil {
			b, _ := json.Marshal(t.InputSchema)
			json.Unmarshal(b, &schema)
		}
		tt := tool{Name: t.Name, Description: t.Description, Schema: schema}
		if t.Annotations != nil {
			tt.Title, tt.ReadOnly = t.Annotations.Title, t.Annotations.ReadOnlyHint
		}
		out = append(out, tt)
	}
	c.mu.Lock()
	c.tools = out
	c.mu.Unlock()
	return out, nil
}

// Call runs one controller tool; the text it returns goes back to the model.
func (c *controller) Call(ctx context.Context, name string, args map[string]any) (string, bool) {
	s, err := c.session(ctx)
	if err != nil {
		return err.Error(), true
	}
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		c.reset()
		return err.Error(), true
	}
	var parts []string
	for _, ct := range res.Content {
		switch v := ct.(type) {
		case *mcp.TextContent:
			parts = append(parts, v.Text)
		case *mcp.ImageContent:
			parts = append(parts, "(an image)")
		}
	}
	out := strings.Join(parts, "\n")
	if len(out) > 20000 {
		out = out[:20000] + "…"
	}
	return out, res.IsError
}
