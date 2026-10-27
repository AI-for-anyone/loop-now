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

// Kind 是同步 Hook 的种类（plugins/SPEC.md §0.2、§3）。
type Kind string

const (
	// Intercept 可阻止：第一个返回 block 的插件生效；超时或出错时放行，清单声明 failClosed 的插件改为阻止。
	Intercept Kind = "intercept"
	// Modify 顺序合并：每个插件看到前面插件改过的事件，patch 按字段合并；超时或出错时跳过该插件。
	Modify Kind = "modify"
	// Claim 认领：第一个返回 handled 的插件生效；超时或出错时当作未处理。
	Claim Kind = "claim"
)

// Outcome 是一次同步调用的结果。
type Outcome struct {
	// Blocked 与 Reason：拦截类被阻止；Plugin 是阻止者。
	Blocked bool
	Reason  string
	Plugin  string
	// Patch 是修改类各插件 patch 按顺序合并的结果；没有修改时为空。
	Patch map[string]any
	// Handled 与 Value：认领类被认领，Value 是认领者返回的整个对象。
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

// Call 按加载顺序把同步事件交给订阅了 event["type"] 的插件，按 kind 的策略汇总。
// include 决定插件是否参与（例如事件所在项目是否启用了它），为空时全部参与。
// 全局停用时没有插件参与；订阅了但当前未加载（加载失败、宿主停止）的插件按超时处理。
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

// Hooker 是业务代码调用同步 Hook 的入口（plugins.Service 实现）：项目内的事件只交给在 project 启用了插件的订阅者，
// project 为空时交给全部订阅者。没有宿主或全局停用时返回空结果。
type Hooker interface {
	Hook(ctx context.Context, kind Kind, event map[string]any, project *uuid.UUID) (Outcome, error)
}

// Event 组装一个同步事件：固定字段（新的 id、type、at、projectId、agentId、taskId、runId）加该类型自己的字段，固定字段优先。
func Event(typ string, project, agent, task, run *uuid.UUID, fields map[string]any) map[string]any {
	ev := maps.Clone(fields)
	if ev == nil {
		ev = map[string]any{}
	}
	ev["id"], ev["type"], ev["at"] = store.NewPluginEventID(), typ, time.Now().UTC().Format(time.RFC3339Nano)
	ev["projectId"], ev["agentId"], ev["taskId"], ev["runId"] = project, agent, task, run
	return ev
}

// BlockedError 是拦截类 Hook 阻止时返回给调用方的错误（409 plugin_blocked）。
func BlockedError(o Outcome) error {
	reason := o.Reason
	if reason == "" {
		reason = "没有说明原因"
	}
	return apperr.Conflict(apperr.CodePluginBlocked, "插件 "+o.Plugin+" 阻止了这个操作："+reason)
}
