package tasks

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
)

// Inside a fork the task has a current node per branch, and the driver runs them at the same time, one goroutine per
// branch (a lane): each claims and runs its branch's turns as the task did one step at a time. The driver itself starts
// the branches (within the limit on branches at once), makes their workspaces, and joins them when all have arrived.

// lanes are the branch goroutines of one drive call.
type lanes struct {
	wg      sync.WaitGroup
	running map[string]bool
	// idle holds, per branch, the poke generation in which its lane last found nothing more to do: it is not started
	// again until the task is poked (new work may have come).
	idle map[string]int
	gen  int
	done chan laneDone
}

type laneDone struct {
	branch string
	gen    int
}

func newLanes() *lanes {
	return &lanes{running: map[string]bool{}, idle: map[string]int{}, done: make(chan laneDone, 64)}
}

// wait waits for every lane to end.
func (l *lanes) wait() {
	stop := make(chan struct{})
	go func() { l.wg.Wait(); close(stop) }()
	for {
		select {
		case <-l.done:
		case <-stop:
			return
		}
	}
}

// start runs the turns of the branch's current node, one after another, until it has none to claim, the branch moves
// past its node (blocked, arrived) or the fork ends.
func (l *lanes) start(ctx context.Context, s *Service, id, project uuid.UUID, branch string) {
	l.running[branch] = true
	gen := l.gen
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		defer func() { l.done <- laneDone{branch, gen} }()
		for ctx.Err() == nil {
			r, err := s.Store.GetTask(ctx, id)
			if err != nil || r.Ended() || r.Fork == nil {
				return
			}
			b, n := r.Fork.BranchRun(branch), branchNode(&r, branch)
			if b == nil || b.State != taskmodel.BranchRunning || n == nil || n.Status == "blocked" || s.larkDispatchPaused(n.RuntimeID) {
				return
			}
			ran, err := s.runTurn(ctx, id, project, n)
			if err != nil {
				if ctx.Err() == nil && s.Log != nil {
					s.Log.Error("task branch coordination failed", "task_id", id, "branch", branch, "error", err)
				}
				return
			}
			if !ran {
				return
			}
		}
	}()
}

// branchNode is the current node of the branch, nil when it has none.
func branchNode(r *taskmodel.Record, branch string) *taskmodel.NodeRun {
	for _, n := range r.Active() {
		if n.Branch != nil && *n.Branch == branch {
			return n
		}
	}
	return nil
}

// driveFork does the next thing a task inside a fork needs: join the branches when all have arrived, start a branch
// the limit allows, make the workspace of a branch whose node waits for it, or run the branches' turns and wait for one
// of them to end (or a poke). more is false when there is nothing to do.
func (s *Service) driveFork(ctx context.Context, id uuid.UUID, r *taskmodel.Record, l *lanes, poke <-chan struct{}) (more bool, err error) {
	f := r.Fork
	if f.Joined() {
		return true, s.join(ctx, id)
	}
	limit := len(f.Branches)
	if p := r.View.Workflow.MaxParallelBranches; p != nil && *p < limit {
		limit = *p
	}
	active := 0
	for _, b := range f.Branches {
		if b.State == taskmodel.BranchStarting || b.State == taskmodel.BranchRunning {
			active++
		}
	}
	for _, b := range f.Branches {
		if b.State == taskmodel.BranchWaiting && active < limit {
			return true, s.startBranch(ctx, id, b.Step)
		}
	}
	for _, b := range f.Branches {
		if n := branchNode(r, b.Step); b.State == taskmodel.BranchStarting && n != nil && n.Status != "blocked" {
			return true, s.prepareBranch(ctx, id, b.Step)
		}
	}
	for _, b := range f.Branches {
		n := branchNode(r, b.Step)
		if b.State != taskmodel.BranchRunning || n == nil || n.Status == "blocked" || l.running[b.Step] {
			continue
		}
		if gen, idle := l.idle[b.Step]; idle && gen == l.gen {
			continue
		}
		l.start(ctx, s, id, r.View.Task.ProjectID, b.Step)
	}
	if len(l.running) == 0 {
		return false, nil
	}
	select {
	case d := <-l.done:
		delete(l.running, d.branch)
		if d.gen == l.gen {
			l.idle[d.branch] = d.gen
		}
	case <-poke:
		l.gen++
	case <-ctx.Done():
		return false, ctx.Err()
	}
	return true, nil
}

// startBranch makes the branch's first node current. Without a workspace to make, its node message is queued at once;
// otherwise prepareBranch does that once the workspace exists.
func (s *Service) startBranch(ctx context.Context, id uuid.UUID, branch string) error {
	_, err := s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		f := m.Record.Fork
		if m.Record.Ended() || f == nil || f.BranchRun(branch) == nil || f.BranchRun(branch).State != taskmodel.BranchWaiting {
			m.Unchanged()
			return nil, nil
		}
		b := f.BranchRun(branch)
		node, e := s.newNode(ctx, m, branch)
		if e != nil {
			return nil, e
		}
		m.Record.AddCurrent(*node)
		b.State = taskmodel.BranchStarting
		if e = m.Emit("node.updated", map[string]any{"node": node}); e != nil {
			return nil, e
		}
		if w := m.Record.View.Workspace; w == nil || w.State != taskmodel.WorkspaceStateActive {
			b.State = taskmodel.BranchRunning
			return nil, s.prepareNode(ctx, m, node, "", "")
		}
		return nil, nil
	})
	return err
}

// prepareBranch makes the branch's workspace (outside the task transaction), records it and queues the node message of
// the branch's node; when the workspace cannot be made, the branch's node is blocked with the reason.
func (s *Service) prepareBranch(ctx context.Context, id uuid.UUID, branch string) error {
	r, err := s.Store.GetTask(ctx, id)
	if err != nil {
		return err
	}
	n := branchNode(&r, branch)
	if n == nil || r.Fork == nil {
		return nil
	}
	made, err := s.makeBranchWorkspace(ctx, r, branch)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		what := "Could not create the branch's worktree: "
		if r.View.Workspace.Mode == taskmodel.WorkspaceModeCopy {
			what = "Could not copy the task's folder for the branch: "
		}
		return s.blockTask(ctx, id, n.ID, what+err.Error())
	}
	_, err = s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		f := m.Record.Fork
		node := branchNode(m.Record, branch)
		if m.Record.Ended() || f == nil || f.BranchRun(branch) == nil || f.BranchRun(branch).State != taskmodel.BranchStarting || node == nil || node.Status == "blocked" {
			m.Unchanged()
			return nil, nil
		}
		w := m.Record.View.Workspace
		if b := w.Branch(branch); b != nil {
			*b = made
		} else {
			w.Branches = append(w.Branches, made)
		}
		f.BranchRun(branch).State = taskmodel.BranchRunning
		return nil, s.prepareNode(ctx, m, node, "", "")
	})
	return err
}

// join ends the fork once every branch has arrived: with a git workspace it first merges the branches into the task
// branch (outside the task transaction), then makes the join step current, its node message saying what the merge did.
func (s *Service) join(ctx context.Context, id uuid.UUID) error {
	r, err := s.Store.GetTask(ctx, id)
	if err != nil {
		return err
	}
	f := r.Fork
	if r.Ended() || f == nil || !f.Joined() {
		return nil
	}
	var out joinResult
	if w := r.View.Workspace; w != nil && w.State == taskmodel.WorkspaceStateActive {
		if w.Mode == taskmodel.WorkspaceModeCopy {
			out = copyJoin(r)
		} else if out, err = s.mergeBranches(ctx, r); err != nil {
			return err
		}
	}
	_, err = s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		cur := m.Record.Fork
		if m.Record.Ended() || cur == nil || cur.ID != f.ID || cur.SourceNodeRunID != f.SourceNodeRunID || !cur.Joined() {
			m.Unchanged()
			return nil, nil
		}
		if w := m.Record.View.Workspace; w != nil {
			for i := range w.Branches {
				b := &w.Branches[i]
				if result, ok := out.merges[b.Step]; ok {
					b.Merge = &result
				}
				if out.removed[b.Step] {
					b.State = taskmodel.WorkspaceStateRemoved
				}
			}
		}
		node, e := s.newNode(ctx, m, cur.Join)
		if e != nil {
			return nil, e
		}
		m.Record.Fork = nil
		m.Record.SetCurrent(*node)
		m.Record.View.Task.Status = "running"
		m.Record.View.Task.Reason = nil
		return nil, s.prepareNode(ctx, m, node, "", out.report)
	})
	return err
}
