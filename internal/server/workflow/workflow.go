// Package workflow defines generic fixed flows, field validation and explicit input mapping. All business nodes and
// fields are defined by the Manager; the package itself does not run Agents or external operations.
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

// Limits are provided by the platform's runtime configuration; the validator itself does not hard-code graph sizes.
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
		return invalid("workflow", "Must have at least one step and stay within the step and edge limits.")
	}
	nodes := map[string]Step{}
	for _, n := range d.Steps {
		if !nodeName.MatchString(n.ID) || nodes[n.ID].ID != "" || strings.TrimSpace(n.Title) == "" || n.AgentID == uuid.Nil {
			return invalid("workflow.steps", "Each step needs a unique id (a letter or _ followed by letters, digits, _ or -, at most 64 characters), a title that is not blank, and an agentId.")
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
				return invalid("workflow.steps.inputs", "Each mapped input must be declared in the step's inputFields.")
			}
			if src.Kind != "request" && src.Kind != "node" || src.Kind == "node" && (src.NodeID == nil || *src.NodeID == "") || src.Kind == "request" && src.NodeID != nil {
				return invalid("workflow.steps.inputs", "Each input source must have kind request without a nodeId, or kind node with a nodeId.")
			}
		}
		nodes[n.ID] = n
	}
	if _, ok := nodes[d.StartStepID]; !ok {
		return invalid("workflow.startStepId", "Must be the id of a step in the workflow.")
	}
	edges := map[string]bool{}
	adj := map[string][]string{}
	unbounded := map[string][]string{}
	for _, e := range d.Edges {
		if !nodeName.MatchString(e.ID) || edges[e.ID] || nodes[e.From].ID == "" || nodes[e.To].ID == "" || e.MaxTraversals != nil && *e.MaxTraversals < 1 {
			return invalid("workflow.edges", "Each edge needs a unique id (a letter or _ followed by letters, digits, _ or -, at most 64 characters), from and to must name existing steps, and maxTraversals must be at least 1 when set.")
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
		return invalid("workflow.steps", "Every step must be reachable from the start step.")
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
			return invalid("workflow.edges", "Every loop must pass through an edge with maxTraversals.")
		}
	}
	for _, n := range d.Steps {
		for _, src := range n.Inputs {
			if src.Kind == "node" {
				prev, ok := nodes[*src.NodeID]
				if !ok {
					return invalid("workflow.steps.inputs", "The source nodeId must name a step in the workflow.")
				}
				if src.Field != "" {
					if _, ok := fieldMap(prev.OutputFields)[src.Field]; !ok {
						return invalid("workflow.steps.inputs", "The source field must be declared in the source step's outputFields.")
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
			return invalid("workflow.fields", "Field names must be unique and consist of a letter or _ followed by letters, digits or _, at most 64 characters.")
		}
		seen[f.Name] = true
		switch f.Type {
		case "string", "number", "integer", "boolean", "object", "array", "artifact":
		default:
			return invalid("workflow.fields", "Field type must be string, number, integer, boolean, object, array or artifact.")
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
			return invalid(key, "This field is not declared.")
		}
	}
	for _, f := range fields {
		v, ok := values[f.Name]
		if !ok || v == nil {
			if f.Required {
				return invalid(f.Name, "This field is required.")
			}
			continue
		}
		if !matches(f.Type, v) {
			return invalid(f.Name, "The value does not match the declared field type.")
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

// The results passed to ResolveInputs must be successful results in the order they succeeded; failed submissions must
// not be passed in. Results are deep-copied, and the returned version links are for tracing in persistence.
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
			return nil, nil, invalid(f.Name, "The input value cannot be encoded as JSON.")
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

// SelectEdge only checks for a valid exit and the limits; it does not evaluate condition expressions and does not
// modify the counts.
func (d Definition) SelectEdge(stepID string, edgeID *string, counts map[string]int) (*Edge, error) {
	edges := d.Outgoing(stepID)
	if len(edges) == 0 {
		if edgeID != nil {
			return nil, invalid("edgeId", "Must be empty: this step has no outgoing edges.")
		}
		return nil, nil
	}
	if edgeID == nil {
		return nil, invalid("edgeId", "Must name one of the step's outgoing edges.")
	}
	for _, e := range edges {
		if e.ID == *edgeID {
			if e.MaxTraversals != nil && counts[e.ID] >= *e.MaxTraversals {
				return nil, invalid("edgeId", "This edge has reached its maxTraversals limit.")
			}
			return &e, nil
		}
	}
	return nil, invalid("edgeId", fmt.Sprintf("Must name an outgoing edge of step %s.", stepID))
}

// integerNumber decides from the decimal literal, without rounding through float64 and without allocating big integers
// for huge exponents.
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
