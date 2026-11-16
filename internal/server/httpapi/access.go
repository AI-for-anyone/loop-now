package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/reqid"
	"loop-now/internal/server/store"
)

// Three entry points:
//   - /api/v1 on the loopback listener: the console, no login; a request with an Authorization header is accepted
//     only with a plugin token (lnp_, limited by x-plugin-scope), anything else is rejected;
//   - /agent/v1 on the loopback listener: Agents, a valid lna_ token is required, requests with an Origin are rejected;
//   - the external listener: external identities, every request must carry a valid lne_ token and may only call
//     routes declared as externally open.
//
// Middleware order: request ID and version headers -> authentication -> permission table -> audit -> handler.

// newMux registers routes under a prefix; each route first checks the permission table, and state-changing requests are
// then audited.
func (s *Server) newMux(prefix string, routes []Route) *http.ServeMux {
	m := http.NewServeMux()
	for _, rt := range routes {
		m.HandleFunc(rt.Method+" "+prefix+rt.Pattern, s.guard(rt))
	}
	m.HandleFunc(prefix+"/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, unknownEndpoint())
	})
	return m
}

// unknownEndpoint is the 404 for a request that matches no endpoint. Its details (reason unknown_endpoint) let clients
// tell an unsupported endpoint from a missing resource without reading the message.
func unknownEndpoint() error {
	e := apperr.NotFound("接口")
	e.Details = apitypes.NotFoundDetails{Reason: apitypes.NotFoundUnknownEndpoint}
	return e
}

// ExternalHandler returns the handler for the external port: the same /api/v1 routes as the console, the plugins'
// /hooks entry point, plus the loopctl downloads (not served when downloads is nil). The external port does not check
// Host or Origin; it relies on tokens and TLS for protection.
func (s *Server) ExternalHandler(downloads http.Handler) http.Handler {
	root := http.NewServeMux()
	root.Handle("/api/v1/", s.newMux("/api/v1", s.Routes()))
	if downloads != nil {
		root.Handle("GET /downloads/loopctl/", downloads)
	}
	root.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, unknownEndpoint())
	})
	// The plugins' HTTP entry point needs no token: the plugin verifies the signature itself (plugins/SPEC.md §11).
	outer := http.NewServeMux()
	outer.Handle("/hooks/{pluginId}", s.pluginIngress("external"))
	outer.Handle("/hooks/{pluginId}/{path...}", s.pluginIngress("external"))
	outer.Handle("/", s.externalAuth(root))
	return withCommonHeaders(s.logRequests(outer))
}

func unauthenticated() error {
	return apperr.Unauthenticated("令牌缺失、无效、已过期或已吊销")
}

// externalAuth verifies an external token and sets the request identity to the external identity the token belongs to.
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

// agentAuth verifies an Agent token and sets the request identity to the Agent the token belongs to; requests with an
// Origin get a 403.
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

// authenticate verifies a Bearer token by prefix: look it up by ID first, then compare the secret in constant time.
// Error messages never echo the token.
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
		s.Log.Warn("failed to record the token's last-used time", "tokenId", id, "err", err)
	}
	return auth.External(t.Identity.ID, t.Identity.Name), nil
}

func unauthenticatedIfNotFound(err error) error {
	if store.IsNotFound(err) {
		return unauthenticated()
	}
	return err
}

// guard wraps a route: external identities may only call routes declared as externally open; state-changing requests
// are audited after they are handled.
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

// auditNote is information a handler adds for the audit: when a plugin calls on behalf of an external identity it
// manages, that identity is recorded.
type auditNote struct{ onBehalfOf *uuid.UUID }

type auditNoteKey struct{}

// noteOnBehalfOf records the external identity this call acts on behalf of (written to the audit).
func noteOnBehalfOf(r *http.Request, identity uuid.UUID) {
	if n, ok := r.Context().Value(auditNoteKey{}).(*auditNote); ok {
		n.onBehalfOf = &identity
	}
}

var pathParam = regexp.MustCompile(`\{(\w+)\}`)

// audit writes one audit entry: time, request ID, actor, operation and path parameters, status code; no request body,
// credentials or tokens. A failed write is only logged.
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
		s.Log.Error("failed to write the audit record", "requestId", reqid.From(r.Context()), "operation", rt.Op, "err", err)
	}
}

// principal returns the identity of the request; without the authentication middleware (only happens in tests) it is
// treated as the console operator.
func principal(r *http.Request) auth.Principal {
	if p, ok := auth.From(r.Context()); ok {
		return p
	}
	return auth.Console()
}
