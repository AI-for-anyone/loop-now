// Package workflow 定义通用的固定流程、字段校验与显式输入映射。
// 所有业务节点和字段均由 Manager 定义，包本身不执行 Agent 或外部操作。
package workflow

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type Field struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

type Source struct {
	Kind   string  `json:"kind"`
	NodeID *string `json:"nodeId,omitempty"`
	Field  string  `json:"field"`
}

type Step struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Description  string            `json:"description"`
	AgentID      uuid.UUID         `json:"agentId"`
	InputFields  []Field           `json:"inputFields"`
	OutputFields []Field           `json:"outputFields"`
	Inputs       map[string]Source `json:"inputs"`
}

type Edge struct {
	ID            string `json:"id"`
	From          string `json:"from"`
	To            string `json:"to"`
	Label         string `json:"label"`
	MaxTraversals *int   `json:"maxTraversals,omitempty"`
}

type Definition struct {
	StartStepID string `json:"startStepId"`
	Steps       []Step `json:"steps"`
	Edges       []Edge `json:"edges"`
}

// Limits 由平台运行配置提供；校验器本身不写死图大小。
type Limits struct{ MaxSteps, MaxEdges int }

type Error struct{ Field, Message string }

func (e *Error) Error() string            { return e.Field + ": " + e.Message }
func invalid(field, message string) error { return &Error{field, message} }

var fieldName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`)
var nodeName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$`)

func (d Definition) Step(id string) (Step, bool) {
	for _, n := range d.Steps {
		if n.ID == id {
			return n, true
		}
	}
	return Step{}, false
}
func (d Definition) Outgoing(id string) []Edge {
	out := []Edge{}
	for _, e := range d.Edges {
		if e.From == id {
			out = append(out, e)
		}
	}
	return out
}

func (d Definition) Validate(l Limits) error {
	if len(d.Steps) == 0 || l.MaxSteps > 0 && len(d.Steps) > l.MaxSteps || l.MaxEdges > 0 && len(d.Edges) > l.MaxEdges {
		return invalid("workflow", "节点/连线数量无效或超限")
	}
	nodes := map[string]Step{}
	for _, n := range d.Steps {
		if !nodeName.MatchString(n.ID) || nodes[n.ID].ID != "" || strings.TrimSpace(n.Title) == "" || n.AgentID == uuid.Nil {
			return invalid("workflow.steps", "节点名称、标题、Agent或唯一性无效")
		}
		if err := validateFields(n.InputFields); err != nil {
			return err
		}
		if err := validateFields(n.OutputFields); err != nil {
			return err
		}
		decl := fieldMap(n.InputFields)
		for name, src := range n.Inputs {
			if _, ok := decl[name]; !ok {
				return invalid("workflow.steps.inputs", "映射指向未声明输入")
			}
			if src.Kind != "request" && src.Kind != "node" || src.Kind == "node" && (src.NodeID == nil || *src.NodeID == "") || src.Kind == "request" && src.NodeID != nil {
				return invalid("workflow.steps.inputs", "输入来源无效")
			}
		}
		nodes[n.ID] = n
	}
	if _, ok := nodes[d.StartStepID]; !ok {
		return invalid("workflow.startStepId", "起始节点不存在")
	}
	edges := map[string]bool{}
	adj := map[string][]string{}
	unbounded := map[string][]string{}
	for _, e := range d.Edges {
		if !nodeName.MatchString(e.ID) || edges[e.ID] || nodes[e.From].ID == "" || nodes[e.To].ID == "" || e.MaxTraversals != nil && *e.MaxTraversals < 1 {
			return invalid("workflow.edges", "连线、节点引用或次数上限无效")
		}
		edges[e.ID] = true
		adj[e.From] = append(adj[e.From], e.To)
		if e.MaxTraversals == nil {
			unbounded[e.From] = append(unbounded[e.From], e.To)
		}
	}
	seen := map[string]bool{}
	var reach func(string)
	reach = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		for _, to := range adj[id] {
			reach(to)
		}
	}
	reach(d.StartStepID)
	if len(seen) != len(nodes) {
		return invalid("workflow.steps", "包含从起点不可达节点")
	}
	color := map[string]int{}
	var cycle func(string) bool
	cycle = func(id string) bool {
		if color[id] == 1 {
			return true
		}
		if color[id] == 2 {
			return false
		}
		color[id] = 1
		for _, to := range unbounded[id] {
			if cycle(to) {
				return true
			}
		}
		color[id] = 2
		return false
	}
	for id := range nodes {
		if cycle(id) {
			return invalid("workflow.edges", "每条回路必须经过有次数上限的连线")
		}
	}
	for _, n := range d.Steps {
		for _, src := range n.Inputs {
			if src.Kind == "node" {
				prev, ok := nodes[*src.NodeID]
				if !ok {
					return invalid("workflow.steps.inputs", "来源节点不存在")
				}
				if src.Field != "" {
					if _, ok := fieldMap(prev.OutputFields)[src.Field]; !ok {
						return invalid("workflow.steps.inputs", "来源输出未声明")
					}
				}
			}
		}
	}
	return nil
}

func validateFields(fields []Field) error {
	seen := map[string]bool{}
	for _, f := range fields {
		if !fieldName.MatchString(f.Name) || seen[f.Name] {
			return invalid("workflow.fields", "字段名称无效或重复")
		}
		seen[f.Name] = true
		switch f.Type {
		case "string", "number", "integer", "boolean", "object", "array", "artifact":
		default:
			return invalid("workflow.fields", "字段类型无效")
		}
	}
	return nil
}
func fieldMap(fields []Field) map[string]Field {
	out := map[string]Field{}
	for _, f := range fields {
		out[f.Name] = f
	}
	return out
}

func ValidateValues(fields []Field, values map[string]any) error {
	decl := fieldMap(fields)
	for key := range values {
		if _, ok := decl[key]; !ok {
			return invalid(key, "未声明的字段")
		}
	}
	for _, f := range fields {
		v, ok := values[f.Name]
		if !ok || v == nil {
			if f.Required {
				return invalid(f.Name, "缺少必需字段")
			}
			continue
		}
		if !matches(f.Type, v) {
			return invalid(f.Name, "字段类型不匹配")
		}
	}
	return nil
}

func matches(kind string, v any) bool {
	switch kind {
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "artifact":
		m, ok := v.(map[string]any)
		if !ok || len(m) != 1 {
			return false
		}
		s, ok := m["artifactId"].(string)
		if !ok {
			return false
		}
		id, err := uuid.Parse(s)
		return err == nil && id != uuid.Nil
	case "number", "integer":
		switch n := v.(type) {
		case json.Number:
			x, e := n.Float64()
			if e != nil || math.IsInf(x, 0) || math.IsNaN(x) {
				return false
			}
			if kind == "integer" {
				return integerNumber(n.String())
			}
			return true
		case float64:
			return !math.IsInf(n, 0) && !math.IsNaN(n) && (kind != "integer" || math.Trunc(n) == n)
		case int, int32, int64, uint, uint32, uint64:
			return true
		}
	}
	return false
}

type SuccessfulResult struct {
	NodeRunID uuid.UUID
	StepID    string
	Output    map[string]any
}
type InputVersion struct {
	Input     string    `json:"input"`
	NodeRunID uuid.UUID `json:"nodeRunId"`
}

// ResolveInputs 的 results 必须是按成功顺序排列的成功结果；失败提交不应传入。
// 结果深拷贝，返回的版本关联供持久化追溯。
func ResolveInputs(step Step, request map[string]any, results []SuccessfulResult) (map[string]any, []InputVersion, error) {
	latest := map[string]SuccessfulResult{}
	for _, r := range results {
		latest[r.StepID] = r
	}
	out := map[string]any{}
	versions := []InputVersion{}
	for _, f := range step.InputFields {
		src, mapped := step.Inputs[f.Name]
		if !mapped {
			continue
		}
		var data map[string]any
		var resultID uuid.UUID
		if src.Kind == "request" {
			data = request
		} else if src.Kind == "node" && src.NodeID != nil {
			if r, ok := latest[*src.NodeID]; ok {
				data = r.Output
				resultID = r.NodeRunID
			}
		}
		var v any
		var exists bool
		if data != nil {
			if src.Field == "" {
				v, exists = data, true
			} else {
				v, exists = data[src.Field]
			}
		}
		if !exists {
			continue
		}
		copy, err := CloneValue(v)
		if err != nil {
			return nil, nil, invalid(f.Name, "输入不可编码")
		}
		out[f.Name] = copy
		if resultID != uuid.Nil {
			versions = append(versions, InputVersion{f.Name, resultID})
		}
	}
	if err := ValidateValues(step.InputFields, out); err != nil {
		return nil, nil, err
	}
	return out, versions, nil
}

func CloneValue(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	err = d.Decode(&out)
	return out, err
}

// SelectEdge 只判断合法出口和上限，不执行条件表达式，不修改计数。
func (d Definition) SelectEdge(stepID string, edgeID *string, counts map[string]int) (*Edge, error) {
	edges := d.Outgoing(stepID)
	if len(edges) == 0 {
		if edgeID != nil {
			return nil, invalid("edgeId", "末节点出口必须为空")
		}
		return nil, nil
	}
	if edgeID == nil {
		return nil, invalid("edgeId", "必须选择节点出口")
	}
	for _, e := range edges {
		if e.ID == *edgeID {
			if e.MaxTraversals != nil && counts[e.ID] >= *e.MaxTraversals {
				return nil, invalid("edgeId", "回路次数超过上限")
			}
			return &e, nil
		}
	}
	return nil, invalid("edgeId", fmt.Sprintf("连线不属于当前节点 %s", stepID))
}

// integerNumber按十进制字面值判断，不经float64舍入，也不为巨大指数分配大整数。
func integerNumber(raw string) bool {
	mantissa, exponent, hasExp := strings.Cut(strings.ToLower(raw), "e")
	digits := strings.ReplaceAll(strings.TrimPrefix(mantissa, "-"), ".", "")
	if strings.Trim(digits, "0") == "" {
		return true
	}
	var exp int64
	if hasExp {
		var e error
		exp, e = strconv.ParseInt(exponent, 10, 64)
		if e != nil {
			return false
		}
	}
	fractional := 0
	if _, tail, ok := strings.Cut(mantissa, "."); ok {
		fractional = len(tail)
	}
	if exp >= int64(fractional) {
		return true
	}
	if exp < -int64(len(digits)) {
		return false
	}
	trailing := len(digits) - len(strings.TrimRight(digits, "0"))
	return int64(fractional) <= exp+int64(trailing)
}
