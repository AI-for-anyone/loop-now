package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
)

// Kind is the kind of a synchronous hook (plugins/SPEC.md §0.2, §3).
type Kind string

const (
	// Intercept can block: the first plugin that returns block wins; on timeout or error the operation is allowed,
	// except that a plugin whose manifest declares failClosed blocks instead.
	Intercept Kind = "intercept"
	// Modify merges in order: each plugin sees the event as changed by the plugins before it, and patches are merged
	// field by field; on timeout or error that plugin is skipped.
	Modify Kind = "modify"
	// Claim claims: the first plugin that returns handled wins; on timeout or error it counts as not handled.
	Claim Kind = "claim"
)

// Outcome is the result of one synchronous call.
type Outcome struct {
	// Blocked and Reason: an intercept was blocked; Plugin is the plugin that blocked it.
	Blocked bool
	Reason  string
	Plugin  string
	// Patch is the result of merging the modify plugins' patches in order; nil when nothing was changed.
	Patch map[string]any
	// Handled and Value: a claim was claimed; Value is the whole object returned by the claiming plugin.
	Handled bool
	Value   json.RawMessage
}

type interceptResult struct {
	Block  bool   `json:"block"`
	Reason string `json:"reason"`
}

type modifyResult struct {
	Patch map[string]any `json:"patch"`
}

type claimResult struct {
	Handled bool `json:"handled"`
}

// Call hands a synchronous event, in load order, to the plugins subscribed to event["type"] and combines the results
// using the policy of kind. include decides whether a plugin takes part (for example, whether the event's project has
// it enabled); when nil, all take part. When plugins are disabled globally no plugin takes part; a plugin that is
// subscribed but not currently loaded (load failed, plugin host stopped) is treated as a timeout.
func (s *Supervisor) Call(ctx context.Context, kind Kind, event map[string]any, include func(plugin string) bool) (Outcome, error) {
	typ, _ := event["type"].(string)
	if typ == "" {
		return Outcome{}, errors.New("事件缺少 type")
	}
	s.mu.Lock()
	if s.disabled {
		s.mu.Unlock()
		return Outcome{}, nil
	}
	c := s.conn
	type target struct {
		spec   Spec
		loaded bool
	}
	var targets []target
	for _, spec := range s.known {
		if slices.Contains(spec.Events, typ) {
			targets = append(targets, target{spec, s.loaded[spec.ID] != nil})
		}
	}
	s.mu.Unlock()

	out := Outcome{}
	current := maps.Clone(event)
	for _, t := range targets {
		if include != nil && !include(t.spec.ID) {
			continue
		}
		var raw json.RawMessage
		err := ErrNotLoaded
		if t.loaded && c != nil {
			payload := map[string]any{"kind": kind, "event": current, "timeoutMs": s.o.CallTimeout.Milliseconds()}
			raw, err = c.request(ctx, TypeCall, t.spec.ID, payload, s.o.CallTimeout, inflight{EventType: typ})
		}
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if err != nil {
			s.enqueueLog(t.spec.ID, "warn", fmt.Sprintf("%s 未得到回应，按%s处理：%v", typ, fallbackName(kind, t.spec.FailClosed), err))
			if kind == Intercept && t.spec.FailClosed {
				return Outcome{Blocked: true, Plugin: t.spec.ID, Reason: fmt.Sprintf("插件 %s 未能检查（%v）", t.spec.ID, err)}, nil
			}
			continue
		}
		switch kind {
		case Intercept:
			var r interceptResult
			if len(raw) > 0 && json.Unmarshal(raw, &r) == nil && r.Block {
				return Outcome{Blocked: true, Plugin: t.spec.ID, Reason: r.Reason}, nil
			}
		case Modify:
			var r modifyResult
			if len(raw) > 0 && json.Unmarshal(raw, &r) == nil && len(r.Patch) > 0 {
				if out.Patch == nil {
					out.Patch = map[string]any{}
				}
				for k, v := range r.Patch {
					out.Patch[k] = v
					current[k] = v
				}
			}
		case Claim:
			var r claimResult
			if len(raw) > 0 && json.Unmarshal(raw, &r) == nil && r.Handled {
				return Outcome{Handled: true, Plugin: t.spec.ID, Value: raw}, nil
			}
		}
	}
	return out, nil
}

func fallbackName(kind Kind, failClosed bool) string {
	switch {
	case kind == Intercept && failClosed:
		return "阻止"
	case kind == Intercept:
		return "放行"
	case kind == Modify:
		return "跳过"
	default:
		return "未处理"
	}
}

// Hooker is the entry point business code uses to call synchronous hooks (implemented by plugins.Service): an event
// inside a project only goes to subscribers that have the plugin enabled in project; when project is nil it goes to
// all subscribers. Without a plugin host, or when plugins are disabled globally, it returns an empty result.
type Hooker interface {
	Hook(ctx context.Context, kind Kind, event map[string]any, project *uuid.UUID) (Outcome, error)
}

// Event assembles a synchronous event: the fixed fields (a new id, type, at, projectId, agentId, taskId, runId) plus
// the fields of that event type; the fixed fields take precedence.
func Event(typ string, project, agent, task, run *uuid.UUID, fields map[string]any) map[string]any {
	ev := maps.Clone(fields)
	if ev == nil {
		ev = map[string]any{}
	}
	ev["id"], ev["type"], ev["at"] = store.NewPluginEventID(), typ, time.Now().UTC().Format(time.RFC3339Nano)
	ev["projectId"], ev["agentId"], ev["taskId"], ev["runId"] = project, agent, task, run
	return ev
}

// BlockedError is the error returned to the caller when an intercept hook blocks (409 plugin_blocked).
func BlockedError(o Outcome) error {
	reason := o.Reason
	if reason == "" {
		reason = "没有说明原因"
	}
	return apperr.Conflict(apperr.CodePluginBlocked, "插件 "+o.Plugin+" 阻止了这个操作："+reason)
}
