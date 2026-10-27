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

// 控制台操作按钮与 HTTP 入口（plugins/SPEC.md §11）：Go 把请求交给宿主里的插件，30 秒内的回应原样返回。

// DefaultRequestTimeout 是 action 与 http 请求等待插件回应的默认时长（Service.RequestTimeout）。
const DefaultRequestTimeout = 30 * time.Second

// hostRequest 向已加载的插件发请求；把宿主的失败换成接口错误：插件不在宿主里时为 notLoaded，
// 超时 504 plugin_timeout，插件报错 502 plugin_failed。
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
		return nil, &apperr.Error{Status: http.StatusGatewayTimeout, Code: apperr.CodePluginTimeout, Message: "插件 " + plugin + " 没有在 " + s.RequestTimeout.String() + " 内回应"}
	case errors.As(err, &pe):
		return nil, &apperr.Error{Status: http.StatusBadGateway, Code: apperr.CodePluginFailed, Message: "插件 " + plugin + "：" + pe.Message}
	case errors.Is(err, pluginhost.ErrHostExited):
		return nil, &apperr.Error{Status: http.StatusBadGateway, Code: apperr.CodePluginFailed, Message: "插件宿主在处理请求时退出"}
	}
	return nil, err
}

// ---- 操作按钮 ----

// ActionWhere 是按钮所在的页面对象（接口里 PluginActionInput.where）。
type ActionWhere struct {
	ProjectID  *uuid.UUID `json:"projectId,omitempty"`
	AgentID    *uuid.UUID `json:"agentId,omitempty"`
	TaskID     *uuid.UUID `json:"taskId,omitempty"`
	IdentityID *uuid.UUID `json:"identityId,omitempty"`
}

// ActionResult 是接口里的 PluginActionResult。
type ActionResult struct {
	Notice string  `json:"notice"`
	URL    *string `json:"url"`
}

// Action 执行插件的操作按钮：操作须在清单 actions 里且 where 的页面种类在它的 where 内，input 按 fields 校验；
// where 所在的项目（Agent、任务所在的项目）须启用了插件。
func (s *Service) Action(ctx context.Context, plugin, action string, where ActionWhere, input map[string]any) (ActionResult, error) {
	_, m, err := s.get(ctx, plugin)
	if err != nil {
		return ActionResult{}, err
	}
	i := slices.IndexFunc(m.Components.Actions, func(a Action) bool { return a.ID == action })
	if i < 0 {
		return ActionResult{}, apperr.NotFound("操作")
	}
	a := m.Components.Actions[i]
	kind, project, err := s.actionScope(ctx, where)
	if err != nil {
		return ActionResult{}, err
	}
	if !slices.Contains(a.Where, kind) {
		return ActionResult{}, apperr.Invalid("where", "操作 "+action+" 不在"+kind+"页面上")
	}
	if err := validateAgainst(a.Fields, true, input, "input"); err != nil {
		return ActionResult{}, err
	}
	notEnabled := apperr.Conflict(apperr.CodePluginNotEnabled, "插件 "+plugin+" 没有在运行")
	if project != nil {
		on, err := s.Store.PluginEnabledIn(ctx, plugin, *project)
		if err != nil {
			return ActionResult{}, err
		}
		if !on {
			return ActionResult{}, apperr.Conflict(apperr.CodePluginNotEnabled, "插件 "+plugin+" 没有在这个项目里启用")
		}
	}
	raw, err := s.hostRequest(ctx, pluginhost.TypeAction, plugin,
		map[string]any{"actionId": action, "where": where, "input": input, "timeoutMs": s.RequestTimeout.Milliseconds()}, notEnabled)
	if err != nil {
		return ActionResult{}, err
	}
	var out ActionResult
	if err := json.Unmarshal(raw, &out); err != nil || out.Notice == "" {
		return ActionResult{}, &apperr.Error{Status: http.StatusBadGateway, Code: apperr.CodePluginFailed, Message: "插件 " + plugin + " 的回应缺少 notice"}
	}
	return out, nil
}

// actionScope 返回 where 的页面种类与所在的项目（平台 Agent 与外部身份、全局页面没有项目）；对象不存在时 404。
func (s *Service) actionScope(ctx context.Context, w ActionWhere) (string, *uuid.UUID, error) {
	switch {
	case w.IdentityID != nil:
		x, err := s.Store.GetExternalIdentity(ctx, *w.IdentityID)
		if err == nil && x.DeletedAt != nil {
			err = apperr.NotFound("外部身份")
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

// ---- HTTP 入口 ----

// MaxHTTPBody 是 HTTP 入口请求正文的上限。
const MaxHTTPBody = 1 << 20

// HTTPRequest 是交给插件的一次 HTTP 请求（宿主 http 请求的载荷）。
type HTTPRequest struct {
	Via     string              `json:"via"` // external（对外端口 /hooks）或 console（回环 /api/v1/plugins/<id>/http）
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Query   string              `json:"query"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}

// HTTPResponse 是插件的回应，原样返回给调用方。
type HTTPResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body"`
}

// hopHeaders 是不转发的逐跳头与由服务端决定的头。
var hopHeaders = []string{"connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "proxy-connection", "te", "trailer",
	"transfer-encoding", "upgrade", "content-length"}

// HTTPRequestHeaders 把调用方的请求头整理成交给插件的形式：头名小写，去掉 Authorization、Cookie 与逐跳头。
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

// HTTP 把请求交给声明了 http: true 的插件；插件不存在、没有 HTTP 入口或不在宿主里时 404，
// 每个插件每秒最多 HTTPRate 次（超出 429）。
func (s *Service) HTTP(ctx context.Context, plugin string, req HTTPRequest) (HTTPResponse, error) {
	_, m, err := s.get(ctx, plugin)
	if err != nil || !m.Components.HTTP {
		if err == nil || store.IsNotFound(err) {
			err = apperr.NotFound("插件 HTTP 入口")
		}
		return HTTPResponse{}, err
	}
	if !s.limiter.allow(plugin, time.Now()) {
		return HTTPResponse{}, &apperr.Error{Status: http.StatusTooManyRequests, Code: apperr.CodeRateLimited, Message: "插件 " + plugin + " 的 HTTP 入口请求过多"}
	}
	payload := struct {
		HTTPRequest
		TimeoutMs int64 `json:"timeoutMs"`
	}{req, s.RequestTimeout.Milliseconds()}
	raw, err := s.hostRequest(ctx, pluginhost.TypeHTTP, plugin, payload, apperr.NotFound("插件 HTTP 入口"))
	if err != nil {
		return HTTPResponse{}, err
	}
	var out HTTPResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return HTTPResponse{}, &apperr.Error{Status: http.StatusBadGateway, Code: apperr.CodePluginFailed, Message: "插件 " + plugin + " 的回应无法解析"}
	}
	if out.Status == 0 {
		out.Status = http.StatusOK
	}
	if out.Status < 100 || out.Status > 599 {
		return HTTPResponse{}, &apperr.Error{Status: http.StatusBadGateway, Code: apperr.CodePluginFailed, Message: "插件 " + plugin + " 回应的状态码不合法"}
	}
	for k := range out.Headers {
		if slices.Contains(hopHeaders, strings.ToLower(k)) {
			delete(out.Headers, k)
		}
	}
	return out, nil
}

// HTTPRate 是每个插件 HTTP 入口每秒允许的请求数（令牌桶，容量同为 HTTPRate）。
const HTTPRate = 20

// rateLimiter 是按插件的令牌桶。
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
