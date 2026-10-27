package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/reqid"
	"loop-now/internal/server/store"
)

// 三个入口：
//   - 回环监听器上的 /api/v1：控制台，免登录；带 Authorization 头的请求只接受插件令牌（lnp_，按 x-plugin-scope 限定），其余一律拒绝；
//   - 回环监听器上的 /agent/v1：Agent，必须带有效的 lna_ 令牌，拒绝带 Origin 的请求；
//   - 对外监听器：外部身份，每个请求都必须带有效的 lne_ 令牌，只能调用声明为对外开放的路由。
//
// 中间件顺序：请求 ID 与版本头 → 身份认证 → 权限表 → 审计 → 处理函数。

// newMux 按前缀注册路由；每条路由先检查权限表，改变状态的请求再记审计。
func (s *Server) newMux(prefix string, routes []Route) *http.ServeMux {
	m := http.NewServeMux()
	for _, rt := range routes {
		m.HandleFunc(rt.Method+" "+prefix+rt.Pattern, s.guard(rt))
	}
	m.HandleFunc(prefix+"/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, apperr.NotFound("接口"))
	})
	return m
}

// ExternalHandler 返回对外端口的处理器：与控制台同一组 /api/v1 路由、插件的 /hooks 入口，外加 loopctl 下载（downloads 为空时不提供）。
// 对外端口不校验 Host 与 Origin，靠令牌与 TLS 保护。
func (s *Server) ExternalHandler(downloads http.Handler) http.Handler {
	root := http.NewServeMux()
	root.Handle("/api/v1/", s.newMux("/api/v1", s.Routes()))
	if downloads != nil {
		root.Handle("GET /downloads/loopctl/", downloads)
	}
	root.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, apperr.NotFound("接口"))
	})
	// 插件的 HTTP 入口不要令牌：签名由插件自己校验（plugins/SPEC.md §11）。
	outer := http.NewServeMux()
	outer.Handle("/hooks/{pluginId}", s.pluginIngress("external"))
	outer.Handle("/hooks/{pluginId}/{path...}", s.pluginIngress("external"))
	outer.Handle("/", s.externalAuth(root))
	return withCommonHeaders(s.logRequests(outer))
}

func unauthenticated() error {
	return apperr.Unauthenticated("令牌缺失、无效、已过期或已吊销")
}

// externalAuth 校验外部令牌，把请求的身份设为令牌所属的外部身份。
func (s *Server) externalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := s.authenticate(r, auth.ExternalPrefix)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.With(r.Context(), p)))
	})
}

// agentAuth 校验 Agent 令牌，把请求的身份设为令牌所属的 Agent；带 Origin 的请求返回 403。
func (s *Server) agentAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			writeError(w, &apperr.Error{Status: http.StatusForbidden, Code: apperr.CodeForbiddenOrigin, Message: "Agent 接口不接受带 Origin 的请求"})
			return
		}
		p, err := s.authenticate(r, auth.AgentPrefix)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.With(r.Context(), p)))
	})
}

// authenticate 按前缀校验 Bearer 令牌：先按 ID 查表，再用常量时间比较密钥。错误信息不回显令牌。
func (s *Server) authenticate(r *http.Request, prefix string) (auth.Principal, error) {
	ctx := r.Context()
	tok, _ := auth.BearerToken(r)
	p, id, secret, ok := auth.ParseToken(tok)
	if !ok || p != prefix {
		return auth.Principal{}, unauthenticated()
	}
	if prefix == auth.AgentPrefix {
		t, err := s.Store.LookupAgentToken(ctx, id)
		if err != nil {
			return auth.Principal{}, unauthenticatedIfNotFound(err)
		}
		if t.Revoked || !auth.SecretMatches(t.Hash, secret) {
			return auth.Principal{}, unauthenticated()
		}
		a, err := s.Store.GetAddressableAgent(ctx, t.AgentID)
		if err != nil {
			return auth.Principal{}, unauthenticatedIfNotFound(err)
		}
		addr, _ := a.Address()
		principal := auth.Principal{Kind: auth.KindAgent, ID: a.ID, Address: addr, Name: a.Name}
		if t.RuntimeID != nil && t.TaskID != nil {
			principal.RuntimeID = *t.RuntimeID
			principal.TaskID = *t.TaskID
			principal.Address += "#" + t.TaskID.String()
		}
		return principal, nil
	}
	t, err := s.Store.LookupExternalToken(ctx, id)
	if err != nil {
		return auth.Principal{}, unauthenticatedIfNotFound(err)
	}
	expired := t.Token.ExpiresAt != nil && !t.Token.ExpiresAt.After(time.Now())
	if !auth.SecretMatches(t.Hash, secret) || t.Token.RevokedAt != nil || expired || t.Identity.DeletedAt != nil {
		return auth.Principal{}, unauthenticated()
	}
	if err := s.Store.TouchExternalToken(ctx, id); err != nil {
		s.Log.Warn("记录令牌最近使用时间失败", "tokenId", id, "err", err)
	}
	return auth.External(t.Identity.ID, t.Identity.Name), nil
}

func unauthenticatedIfNotFound(err error) error {
	if store.IsNotFound(err) {
		return unauthenticated()
	}
	return err
}

// guard 包装一条路由：外部身份只能调用声明为对外开放的路由；改变状态的请求在处理后记审计。
func (s *Server) guard(rt Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := auth.From(r.Context())
		if p.Kind == auth.KindExternal && rt.Access != ConsoleAndExternal {
			writeError(w, apperr.Forbidden("外部身份不能使用这个接口"))
			return
		}
		if p.Kind == auth.KindPlugin {
			if err := s.pluginAllowed(r, rt, p); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			rt.handler(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		r = r.WithContext(context.WithValue(r.Context(), auditNoteKey{}, &auditNote{}))
		rt.handler(rec, r)
		s.audit(r, rt, p, rec.status)
	}
}

// auditNote 是处理函数补给审计的信息：插件以代管的外部身份的名义调用时记下那个身份。
type auditNote struct{ onBehalfOf *uuid.UUID }

type auditNoteKey struct{}

// noteOnBehalfOf 记下这次调用代表的外部身份（写进审计）。
func noteOnBehalfOf(r *http.Request, identity uuid.UUID) {
	if n, ok := r.Context().Value(auditNoteKey{}).(*auditNote); ok {
		n.onBehalfOf = &identity
	}
}

var pathParam = regexp.MustCompile(`\{(\w+)\}`)

// audit 写一条审计：时间、请求 ID、操作者、操作与路径参数、状态码；不含请求体、凭证与令牌。写入失败只记日志。
func (s *Server) audit(r *http.Request, rt Route, p auth.Principal, status int) {
	e := store.AuditEntry{ActorKind: string(p.Kind), ActorAddress: p.Address, Method: r.Method, Operation: rt.Op,
		Target: map[string]string{}, Status: status}
	if n, ok := r.Context().Value(auditNoteKey{}).(*auditNote); ok {
		e.OnBehalfOf = n.onBehalfOf
	}
	if id, err := uuid.Parse(reqid.From(r.Context())); err == nil {
		e.RequestID = &id
	}
	if p.ID != uuid.Nil {
		id := p.ID
		e.ActorID = &id
	}
	for _, m := range pathParam.FindAllStringSubmatch(rt.Pattern, -1) {
		e.Target[m[1]] = r.PathValue(m[1])
	}
	if err := s.Store.InsertAudit(context.WithoutCancel(r.Context()), e); err != nil {
		s.Log.Error("写入审计失败", "requestId", reqid.From(r.Context()), "operation", rt.Op, "err", err)
	}
}

// principal 返回请求的身份；没有经过认证中间件时（只在测试中出现）视为控制台操作者。
func principal(r *http.Request) auth.Principal {
	if p, ok := auth.From(r.Context()); ok {
		return p
	}
	return auth.Console()
}
