package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/store"
)

// Console action buttons and the HTTP entry point (plugins/SPEC.md §11): Go hands the request to the plugin in the
// plugin host and returns its response as-is if it arrives within 30 seconds.

// DefaultRequestTimeout is the default time action and http requests wait for the plugin to respond
// (Service.RequestTimeout).
const DefaultRequestTimeout = 30 * time.Second

// hostRequest sends a request to a loaded plugin and turns plugin host failures into API errors: notLoaded when the
// plugin is not in the host, 504 plugin_timeout on timeout, and 502 plugin_failed when the plugin reports an error.
func (s *Service) hostRequest(ctx context.Context, typ, plugin string, payload any, notLoaded error) (json.RawMessage, error) {
	if s.Host == nil {
		return nil, notLoaded
	}
	raw, err := s.Host.Request(ctx, typ, plugin, payload, s.RequestTimeout)
	var pe *pluginhost.PluginError
	switch {
	case err == nil:
		return raw, nil
	case errors.Is(err, pluginhost.ErrNotLoaded):
		return nil, notLoaded
	case errors.Is(err, pluginhost.ErrTimeout):
		return nil, &apperr.Error{Status: http.StatusGatewayTimeout, Code: apperr.CodePluginTimeout, Message: "Plugin " + plugin + " did not respond within " + s.RequestTimeout.String() + "."}
	case errors.As(err, &pe):
		return nil, &apperr.Error{Status: http.StatusBadGateway, Code: apperr.CodePluginFailed, Message: "Plugin " + plugin + ": " + pe.Message}
	case errors.Is(err, pluginhost.ErrHostExited):
		return nil, &apperr.Error{Status: http.StatusBadGateway, Code: apperr.CodePluginFailed, Message: "The plugin host exited while handling the request."}
	}
	return nil, err
}

// ---- Action buttons ----

// ActionWhere is the page object the button is on (PluginActionInput.where in the API).
type ActionWhere struct {
	ProjectID  *uuid.UUID `json:"projectId,omitempty"`
	AgentID    *uuid.UUID `json:"agentId,omitempty"`
	TaskID     *uuid.UUID `json:"taskId,omitempty"`
	IdentityID *uuid.UUID `json:"identityId,omitempty"`
}

// ActionResult is the API's PluginActionResult.
type ActionResult struct {
	Notice string  `json:"notice"`
	URL    *string `json:"url"`
}

// Action runs a plugin's action button: the action must be in the manifest's actions and the page kind of where must
// be in its where; input is validated against fields. The project of where (the project the Agent or task belongs
// to) must have the plugin enabled.
func (s *Service) Action(ctx context.Context, plugin, action string, where ActionWhere, input map[string]any) (ActionResult, error) {
	_, m, err := s.get(ctx, plugin)
	if err != nil {
		return ActionResult{}, err
	}
	i := slices.IndexFunc(m.Components.Actions, func(a Action) bool { return a.ID == action })
	if i < 0 {
		return ActionResult{}, apperr.NotFound("Action")
	}
	a := m.Components.Actions[i]
	kind, project, err := s.actionScope(ctx, where)
	if err != nil {
		return ActionResult{}, err
	}
	if !slices.Contains(a.Where, kind) {
		return ActionResult{}, apperr.Invalid("where", "Action "+action+" is not on the "+kind+" page.")
	}
	if err := validateAgainst(a.Fields, true, input, "input"); err != nil {
		return ActionResult{}, err
	}
	notEnabled := apperr.Conflict(apperr.CodePluginNotEnabled, "Plugin "+plugin+" is not running.")
	if project != nil {
		on, err := s.Store.PluginEnabledIn(ctx, plugin, *project)
		if err != nil {
			return ActionResult{}, err
		}
		if !on {
			return ActionResult{}, apperr.Conflict(apperr.CodePluginNotEnabled, "Plugin "+plugin+" is not enabled in this project.")
		}
	}
	raw, err := s.hostRequest(ctx, pluginhost.TypeAction, plugin,
		map[string]any{"actionId": action, "where": where, "input": input, "timeoutMs": s.RequestTimeout.Milliseconds()}, notEnabled)
	if err != nil {
		return ActionResult{}, err
	}
	var out ActionResult
	if err := json.Unmarshal(raw, &out); err != nil || out.Notice == "" {
		return ActionResult{}, &apperr.Error{Status: http.StatusBadGateway, Code: apperr.CodePluginFailed, Message: "Plugin " + plugin + " responded without a notice."}
	}
	return out, nil
}

// actionScope returns the page kind of where and its project (platform Agents, external identities and the global
// page have no project); 404 when the object does not exist.
func (s *Service) actionScope(ctx context.Context, w ActionWhere) (string, *uuid.UUID, error) {
	switch {
	case w.IdentityID != nil:
		x, err := s.Store.GetExternalIdentity(ctx, *w.IdentityID)
		if err == nil && x.DeletedAt != nil {
			err = apperr.NotFound("External identity")
		}
		return "identity", nil, err
	case w.TaskID != nil:
		t, err := s.Store.GetTask(ctx, *w.TaskID)
		if err != nil {
			return "", nil, err
		}
		pid := t.View.Task.ProjectID
		return "task", &pid, nil
	case w.AgentID != nil:
		a, err := s.Store.GetAgent(ctx, *w.AgentID)
		return "agent", a.ProjectID, err
	case w.ProjectID != nil:
		p, err := s.Store.GetProject(ctx, *w.ProjectID)
		return "project", &p.ID, err
	}
	return "global", nil, nil
}

// ---- HTTP entry point ----

// MaxHTTPBody is the limit on the request body of the HTTP entry point.
const MaxHTTPBody = 1 << 20

// HTTPRequest is one HTTP request handed to a plugin (the payload of the plugin host's http request).
type HTTPRequest struct {
	Via     string              `json:"via"` // external (public port /hooks) or console (loopback /api/v1/plugins/<id>/http)
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Query   string              `json:"query"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}

// HTTPResponse is the plugin's response, returned to the caller as-is.
type HTTPResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}

// hopHeaders are the hop-by-hop headers and the headers decided by the server, which are not forwarded.
var hopHeaders = []string{"connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "proxy-connection", "te", "trailer",
	"transfer-encoding", "upgrade", "content-length"}

// HTTPRequestHeaders turns the caller's request headers into the form handed to the plugin: lower-case header
// names, without Authorization, Cookie and hop-by-hop headers.
func HTTPRequestHeaders(h http.Header) map[string][]string {
	out := map[string][]string{}
	for k, v := range h {
		k = strings.ToLower(k)
		if k == "authorization" || k == "cookie" || slices.Contains(hopHeaders, k) {
			continue
		}
		out[k] = v
	}
	return out
}

// HTTP hands the request to a plugin that declares http: true; 404 when the plugin does not exist, has no HTTP
// entry point or is not in the plugin host. Each plugin gets at most HTTPRate requests per second (429 beyond that).
func (s *Service) HTTP(ctx context.Context, plugin string, req HTTPRequest) (HTTPResponse, error) {
	_, m, err := s.get(ctx, plugin)
	if err != nil || !m.Components.HTTP {
		if err == nil || store.IsNotFound(err) {
			err = apperr.NotFound("Plugin HTTP entry point")
		}
		return HTTPResponse{}, err
	}
	if !s.limiter.allow(plugin, time.Now()) {
		return HTTPResponse{}, &apperr.Error{Status: http.StatusTooManyRequests, Code: apperr.CodeRateLimited, Message: "Plugin " + plugin + " is getting too many requests on its HTTP endpoint. Try again later."}
	}
	payload := struct {
		HTTPRequest
		TimeoutMs int64 `json:"timeoutMs"`
	}{req, s.RequestTimeout.Milliseconds()}
	raw, err := s.hostRequest(ctx, pluginhost.TypeHTTP, plugin, payload, apperr.NotFound("Plugin HTTP entry point"))
	if err != nil {
		return HTTPResponse{}, err
	}
	var out HTTPResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return HTTPResponse{}, &apperr.Error{Status: http.StatusBadGateway, Code: apperr.CodePluginFailed, Message: "Plugin " + plugin + " sent a response that could not be parsed."}
	}
	if out.Status == 0 {
		out.Status = http.StatusOK
	}
	if out.Status < 100 || out.Status > 599 {
		return HTTPResponse{}, &apperr.Error{Status: http.StatusBadGateway, Code: apperr.CodePluginFailed, Message: "Plugin " + plugin + " responded with an invalid status code."}
	}
	for k := range out.Headers {
		if slices.Contains(hopHeaders, strings.ToLower(k)) {
			delete(out.Headers, k)
		}
	}
	return out, nil
}

// HTTPRate is the number of requests per second allowed for each plugin's HTTP entry point (a token bucket whose
// capacity is also HTTPRate).
const HTTPRate = 20

// rateLimiter is a per-plugin token bucket.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

func (l *rateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = map[string]*bucket{}
	}
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: HTTPRate, at: now}
		l.buckets[key] = b
	}
	b.tokens = min(HTTPRate, b.tokens+now.Sub(b.at).Seconds()*HTTPRate)
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
