package codexapp

import (
	"context"
	"errors"
)

// InitializeResult is what initialize reports.
type InitializeResult struct {
	UserAgent string `json:"userAgent"`
	CodexHome string `json:"codexHome"`
}

// Initialize opens the connection (initialize, then initialized). It does not ask for the experimental API.
func (c *Client) Initialize(ctx context.Context, name, version string) (InitializeResult, error) {
	var out InitializeResult
	params := map[string]any{"clientInfo": map[string]any{"name": name, "title": name, "version": version}}
	if err := c.Call(ctx, "initialize", params, &out); err != nil {
		return out, err
	}
	if err := c.notify("initialized", nil); err != nil {
		return out, ErrExited
	}
	return out, nil
}

// ThreadOptions are the settings a thread starts, continues or forks with. Prompts are always off: approval policy
// never (the granular form needs the experimental API) and full access.
type ThreadOptions struct {
	Cwd                   string
	Model                 string
	DeveloperInstructions string
	// Config overrides single keys of the user's Codex configuration for this thread (such as mcp_servers.<name>).
	Config map[string]any
}

func (o ThreadOptions) params() map[string]any {
	p := map[string]any{"approvalPolicy": "never", "sandbox": "danger-full-access"}
	if o.Cwd != "" {
		p["cwd"] = o.Cwd
	}
	if o.Model != "" {
		p["model"] = o.Model
	}
	if o.DeveloperInstructions != "" {
		p["developerInstructions"] = o.DeveloperInstructions
	}
	if len(o.Config) > 0 {
		p["config"] = o.Config
	}
	return p
}

type threadResponse struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

// StartThread starts a new thread and returns its id. A thread is saved by Codex only once it has had a turn.
func (c *Client) StartThread(ctx context.Context, o ThreadOptions) (string, error) {
	var out threadResponse
	if err := c.Call(ctx, "thread/start", o.params(), &out); err != nil {
		return "", err
	}
	if out.Thread.ID == "" {
		return "", errors.New("codexapp: thread/start returned no thread id")
	}
	return out.Thread.ID, nil
}

// ResumeThread continues a saved thread; IsThreadNotFound tells when there is none.
func (c *Client) ResumeThread(ctx context.Context, threadID string, o ThreadOptions) error {
	p := o.params()
	p["threadId"] = threadID
	return c.Call(ctx, "thread/resume", p, nil)
}

// ForkThread copies a saved thread up to and including lastTurnID (the whole thread when it is empty) into a new
// thread and returns the new id; the original is left as it is.
func (c *Client) ForkThread(ctx context.Context, threadID, lastTurnID string, o ThreadOptions) (string, error) {
	p := o.params()
	p["threadId"] = threadID
	if lastTurnID != "" {
		p["lastTurnId"] = lastTurnID
	}
	var out threadResponse
	if err := c.Call(ctx, "thread/fork", p, &out); err != nil {
		return "", err
	}
	if out.Thread.ID == "" {
		return "", errors.New("codexapp: thread/fork returned no thread id")
	}
	return out.Thread.ID, nil
}

// LastTurnID returns the id of a saved thread's latest turn; empty when it has none.
func (c *Client) LastTurnID(ctx context.Context, threadID string) (string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.Call(ctx, "thread/turns/list", map[string]any{"threadId": threadID, "limit": 1, "sortDirection": "desc"}, &out); err != nil {
		return "", err
	}
	if len(out.Data) == 0 {
		return "", nil
	}
	return out.Data[0].ID, nil
}

// Archive moves a saved thread to Codex's archived threads (the user can restore it); IsThreadNotFound tells when there
// is none.
func (c *Client) Archive(ctx context.Context, threadID string) error {
	return c.Call(ctx, "thread/archive", map[string]any{"threadId": threadID}, nil)
}

// SetSkillRoots adds directories where Codex finds Skills, for the whole process.
func (c *Client) SetSkillRoots(ctx context.Context, roots []string) error {
	if roots == nil {
		roots = []string{}
	}
	return c.Call(ctx, "skills/extraRoots/set", map[string]any{"extraRoots": roots}, nil)
}

// Model is one model model/list reports.
type Model struct {
	ID              string
	DisplayName     string
	Hidden          bool
	IsDefault       bool
	Efforts         []string
	DefaultEffort   string
	InputModalities []string
}

// ListModels returns every model the login can use, following the pages.
func (c *Client) ListModels(ctx context.Context) ([]Model, error) {
	var out []Model
	cursor := ""
	for range 50 {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Data []struct {
				ID                        string   `json:"id"`
				Model                     string   `json:"model"`
				DisplayName               string   `json:"displayName"`
				Hidden                    bool     `json:"hidden"`
				IsDefault                 bool     `json:"isDefault"`
				DefaultReasoningEffort    string   `json:"defaultReasoningEffort"`
				InputModalities           []string `json:"inputModalities"`
				SupportedReasoningEfforts []struct {
					ReasoningEffort string `json:"reasoningEffort"`
				} `json:"supportedReasoningEfforts"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := c.Call(ctx, "model/list", params, &page); err != nil {
			return nil, err
		}
		for _, m := range page.Data {
			id := m.ID
			if id == "" {
				id = m.Model
			}
			model := Model{ID: id, DisplayName: m.DisplayName, Hidden: m.Hidden, IsDefault: m.IsDefault, DefaultEffort: m.DefaultReasoningEffort, InputModalities: m.InputModalities}
			for _, e := range m.SupportedReasoningEfforts {
				model.Efforts = append(model.Efforts, e.ReasoningEffort)
			}
			out = append(out, model)
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return out, nil
		}
		cursor = *page.NextCursor
	}
	return out, nil
}
