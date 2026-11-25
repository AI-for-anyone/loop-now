package codexapp_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/executor/local/codexapp"
	"loop-now/internal/server/executor/local/runturn"
)

// bgObserver records the commands that outlive their turn.
type bgObserver struct {
	observer
	bgMu    sync.Mutex
	started []codexapp.Command
	ended   chan codexEnd
}

type codexEnd struct {
	c        codexapp.Command
	status   string
	exitCode *int64
	output   *string
}

func newBgObserver() *bgObserver { return &bgObserver{ended: make(chan codexEnd, 4)} }

func (o *bgObserver) CommandBackgrounded(c codexapp.Command) {
	o.bgMu.Lock()
	o.started = append(o.started, c)
	o.bgMu.Unlock()
}
func (o *bgObserver) CommandEnded(c codexapp.Command, status string, exitCode *int64, output *string) {
	o.ended <- codexEnd{c, status, exitCode, output}
}

func waitEnd(t *testing.T, o *bgObserver) codexEnd {
	t.Helper()
	select {
	case e := <-o.ended:
		return e
	case <-time.After(10 * time.Second):
		t.Fatal("the command did not end")
	}
	return codexEnd{}
}

// A command still running when its turn completes is reported as going on in the background; its later
// item/completed ends the tool on the turn that started it and is reported with its exit code and output.
func TestCommandOutlivesTurn(t *testing.T) {
	h := newHarness(t)
	c := h.start()
	obs := newBgObserver()
	c.SetObserver(obs)
	thread, err := c.StartThread(ctx(t), codexapp.ThreadOptions{Cwd: h.home, Model: "gpt-test"})
	if err != nil {
		t.Fatal(err)
	}
	s := &sink{}
	r := turn(t, c, thread, "fake:background 200ms sleep 20; echo finished", s)
	if r.Outcome != runturn.OutcomeCompleted || r.Text != "started" {
		t.Fatalf("the turn completes while the command runs: %+v", r)
	}
	obs.bgMu.Lock()
	started := append([]codexapp.Command(nil), obs.started...)
	obs.bgMu.Unlock()
	if len(started) != 1 || started[0].Command != "sleep 20; echo finished" || started[0].ProcessID != "pty-1" || started[0].ItemID == "" {
		t.Fatalf("backgrounded: %+v", started)
	}
	end := waitEnd(t, obs)
	if end.c.ItemID != started[0].ItemID || end.status != "completed" || end.exitCode == nil || *end.exitCode != 0 || end.output == nil || !strings.HasSuffix(*end.output, "finished\n") {
		t.Fatalf("ended: %+v", end)
	}
	s.mu.Lock()
	tools := strings.Join(s.tools, ",")
	s.mu.Unlock()
	if tools != "start:shell,end:shell:ok" {
		t.Fatalf("the tool ends on the turn that started it: %s", tools)
	}
}

// A command running in the background is terminated with thread/backgroundTerminals/terminate.
func TestTerminateCommand(t *testing.T) {
	h := newHarness(t)
	c := h.start()
	obs := newBgObserver()
	c.SetObserver(obs)
	thread, err := c.StartThread(ctx(t), codexapp.ThreadOptions{Cwd: h.home, Model: "gpt-test"})
	if err != nil {
		t.Fatal(err)
	}
	turn(t, c, thread, "fake:background 30s sleep 60", nil)
	obs.bgMu.Lock()
	item := obs.started[0].ItemID
	obs.bgMu.Unlock()
	if ok, err := c.TerminateCommand(ctx(t), item); err != nil || !ok {
		t.Fatalf("terminate: %v %v", ok, err)
	}
	if end := waitEnd(t, obs); end.c.ItemID != item || end.status != "failed" {
		t.Fatalf("terminated: %+v", end)
	}
	if ok, err := c.TerminateCommand(ctx(t), item); err != nil || ok {
		t.Fatalf("a command no longer running: %v %v", ok, err)
	}
}
