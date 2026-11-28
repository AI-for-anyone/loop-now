package workflow

import "fmt"

// A fork is a set of edges leaving one step that the step takes together by submitting the fork's id; each edge starts a
// branch. A branch is the steps reachable from its first step (the fork edge's target) without passing the fork's join;
// it is named by that first step. Branches run at the same time and meet at the join, which starts once every branch has
// arrived (submitted an edge into it). Forks are not nested: a fork leaves a step outside every branch, and its join lies
// outside every branch, so outside a fork the task does one step at a time, as without forks.

// Sequential reports whether the workflow has no forks: it does one step at a time.
func (d Definition) Sequential() bool { return len(d.Forks) == 0 }

// Fork is the fork with this id.
func (d Definition) Fork(id string) (Fork, bool) {
	for _, f := range d.Forks {
		if f.ID == id {
			return f, true
		}
	}
	return Fork{}, false
}

// ForkEdges are the fork's edges in workflow order: the order its branches start in and are merged at the join.
func (d Definition) ForkEdges(id string) []Edge {
	out := []Edge{}
	for _, e := range d.Edges {
		if e.Fork != "" && e.Fork == id {
			out = append(out, e)
		}
	}
	return out
}

// Branch is the branch the step belongs to, named by the branch's first step; "" for a step outside every branch.
func (d Definition) Branch(stepID string) string {
	lanes, _ := d.branches()
	return lanes[stepID].entry
}

// JoinOf is the join where the branch (named by its first step) ends; "" for a step that starts no branch.
func (d Definition) JoinOf(branch string) string {
	if branch == "" {
		return ""
	}
	lanes, _ := d.branches()
	if l, ok := lanes[branch]; ok && l.entry == branch {
		return l.join
	}
	return ""
}

// Exit is where a step's submission leads: one edge, a fork (all of its edges at once), or, with neither, the end of
// the task.
type Exit struct {
	Edge  *Edge
	Fork  *Fork
	Edges []Edge
}

// SelectExit checks that id names a valid exit of the step within the traversal limits: one of its edges that belongs
// to no fork, or a fork leaving it (each of whose edges must be within its limit), or nothing for a step without
// outgoing edges. It does not modify the counts.
func (d Definition) SelectExit(stepID string, id *string, counts map[string]int) (Exit, error) {
	edges := d.Outgoing(stepID)
	if len(edges) == 0 {
		if id != nil {
			return Exit{}, invalid("edgeId", "Must be empty: this step has no outgoing edges.")
		}
		return Exit{}, nil
	}
	if id == nil {
		return Exit{}, invalid("edgeId", "Must name one of the step's outgoing edges.")
	}
	limited := func(e Edge) error {
		if e.MaxTraversals != nil && counts[e.ID] >= *e.MaxTraversals {
			return invalid("edgeId", "This edge has reached its maxTraversals limit.")
		}
		return nil
	}
	for _, e := range edges {
		if e.ID == *id && e.Fork == "" {
			if err := limited(e); err != nil {
				return Exit{}, err
			}
			return Exit{Edge: &e}, nil
		}
	}
	if f, ok := d.Fork(*id); ok {
		if fe := d.ForkEdges(f.ID); len(fe) > 0 && fe[0].From == stepID {
			for _, e := range fe {
				if err := limited(e); err != nil {
					return Exit{}, invalid("edgeId", fmt.Sprintf("Edge %s of this fork has reached its maxTraversals limit.", e.ID))
				}
			}
			return Exit{Fork: &f, Edges: fe}, nil
		}
	}
	for _, e := range edges {
		if e.ID == *id {
			return Exit{}, invalid("edgeId", fmt.Sprintf("Edge %s belongs to fork %s: submit the fork's id to take all of its edges.", e.ID, e.Fork))
		}
	}
	return Exit{}, invalid("edgeId", fmt.Sprintf("Must name an outgoing edge or fork of step %s.", stepID))
}

// lane is the branch a step is in: its first step and the join it ends at.
type lane struct{ entry, join string }

// branches finds the branch of every step that is in one, and checks the shape of the forks: each fork leaves a step
// outside every branch; a fork edge does not go straight to its join; a branch is entered only through its fork, leads
// only to its own steps or its join, has no end step and no fork, and every step of it reaches the join; branches share
// no steps (a second fork may start the same branch, with the same join).
func (d Definition) branches() (map[string]lane, error) {
	lanes := map[string]lane{}
	forksFrom := map[string][]Fork{}
	for _, f := range d.Forks {
		if fe := d.ForkEdges(f.ID); len(fe) > 0 {
			forksFrom[fe[0].From] = append(forksFrom[fe[0].From], f)
		}
	}
	// The trunk: the steps reached from the start without entering a branch, a fork leading on to its join.
	trunk := map[string]bool{}
	queue := []string{d.StartStepID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if trunk[id] {
			continue
		}
		trunk[id] = true
		for _, e := range d.Outgoing(id) {
			if e.Fork == "" {
				queue = append(queue, e.To)
			}
		}
		for _, f := range forksFrom[id] {
			queue = append(queue, f.Join)
		}
	}
	nested := invalid("workflow.forks", "Forks cannot be nested: a fork must leave a step outside every branch, and its join must lie outside every branch.")
	for _, f := range d.Forks {
		fe := d.ForkEdges(f.ID)
		if len(fe) == 0 {
			continue
		}
		if !trunk[fe[0].From] {
			return nil, nested
		}
		for _, e := range fe {
			if e.To == f.Join {
				return nil, invalid("workflow.forks", fmt.Sprintf("Edge %s of fork %s goes straight to the join: each fork edge must start a branch of at least one step.", e.ID, f.ID))
			}
			branch := map[string]bool{}
			queue := []string{e.To}
			for len(queue) > 0 {
				id := queue[0]
				queue = queue[1:]
				if id == f.Join || branch[id] {
					continue
				}
				branch[id] = true
				for _, out := range d.Outgoing(id) {
					queue = append(queue, out.To)
				}
			}
			// In step order, so the error names the same step every time.
			for _, s := range d.Steps {
				id := s.ID
				if !branch[id] {
					continue
				}
				if trunk[id] {
					return nil, invalid("workflow.forks", fmt.Sprintf("Step %s is in the branch of fork %s that starts at %s and is also reached from outside it: a branch is entered only through its fork, and its edges lead only to its own steps or to the fork's join (%s).", id, f.ID, e.To, f.Join))
				}
				out := d.Outgoing(id)
				if len(out) == 0 {
					return nil, invalid("workflow.forks", fmt.Sprintf("Step %s in a branch of fork %s has no outgoing edges: a branch cannot end the task; every step of it must lead to the join %s.", id, f.ID, f.Join))
				}
				for _, o := range out {
					if o.Fork != "" {
						return nil, nested
					}
				}
				if l, ok := lanes[id]; ok && l != (lane{e.To, f.Join}) {
					return nil, invalid("workflow.forks", fmt.Sprintf("Step %s is in two branches (starting at %s and at %s): branches must not share steps.", id, l.entry, e.To))
				}
				lanes[id] = lane{e.To, f.Join}
			}
			// Every step of the branch must reach the join.
			reached := map[string]bool{f.Join: true}
			for grew := true; grew; {
				grew = false
				for id := range branch {
					if reached[id] {
						continue
					}
					for _, o := range d.Outgoing(id) {
						if reached[o.To] {
							reached[id], grew = true, true
							break
						}
					}
				}
			}
			for _, s := range d.Steps {
				if id := s.ID; branch[id] && !reached[id] {
					return nil, invalid("workflow.forks", fmt.Sprintf("Step %s in a branch of fork %s cannot reach the join %s.", id, f.ID, f.Join))
				}
			}
		}
	}
	return lanes, nil
}

// validateForkDeclarations checks the forks as declared: valid unique ids that no edge uses, a join that is a step,
// every edge's fork declared, and each fork made of at least two edges leaving the same step.
func (d Definition) validateForkDeclarations(steps map[string]Step, edges map[string]bool) error {
	if d.MaxParallelBranches != nil && *d.MaxParallelBranches < 1 {
		return invalid("workflow.maxParallelBranches", "Must be at least 1 when set.")
	}
	forks := map[string]bool{}
	for _, f := range d.Forks {
		if !nodeName.MatchString(f.ID) || forks[f.ID] || edges[f.ID] {
			return invalid("workflow.forks", "Each fork needs a unique id (a letter or _ followed by letters, digits, _ or -, at most 64 characters) that is not also an edge id.")
		}
		if steps[f.Join].ID == "" {
			return invalid("workflow.forks", fmt.Sprintf("The join of fork %s must name a step in the workflow.", f.ID))
		}
		forks[f.ID] = true
	}
	for _, e := range d.Edges {
		if e.Fork != "" && !forks[e.Fork] {
			return invalid("workflow.edges", fmt.Sprintf("Edge %s names fork %s, which workflow.forks does not declare.", e.ID, e.Fork))
		}
	}
	for _, f := range d.Forks {
		fe := d.ForkEdges(f.ID)
		if len(fe) < 2 {
			return invalid("workflow.forks", fmt.Sprintf("Fork %s needs at least two edges.", f.ID))
		}
		for _, e := range fe {
			if e.From != fe[0].From {
				return invalid("workflow.forks", fmt.Sprintf("The edges of fork %s must all leave the same step.", f.ID))
			}
		}
	}
	return nil
}

// validateBranchAgents checks that an Agent doing a step in a branch does steps only in that branch: a task has one
// instance per Agent, with one conversation and one working folder, and branches run at the same time.
func (d Definition) validateBranchAgents(lanes map[string]lane) error {
	where := map[string]string{}
	for _, s := range d.Steps {
		key := s.AgentID.String()
		branch := lanes[s.ID].entry
		prev, seen := where[key]
		if !seen {
			where[key] = branch
			continue
		}
		if prev != branch {
			return invalid("workflow.steps.agentId", fmt.Sprintf("Step %s: an Agent that does a step in a parallel branch can do steps only in that branch (a task has one instance per Agent, and branches run at the same time); give it another Agent.", s.ID))
		}
	}
	return nil
}
