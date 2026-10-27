package conversation_test

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestForkGateWaitsRoundAndPausesNewDispatch(t *testing.T) {
	e := newEnv(t)
	e.mustSend("block current", "")
	e.waitStarted("block current")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if release, err := e.svc.PauseDispatch(ctx, e.agent); !errors.Is(err, context.DeadlineExceeded) {
		if release != nil {
			release()
		}
		t.Fatal("fork gate bypassed running round", err)
	}
	if !e.ex.signal(e.agent, "release") {
		t.Fatal("normal round not running")
	}
	e.waitIdle()
	release, err := e.svc.PauseDispatch(context.Background(), e.agent)
	if err != nil {
		t.Fatal(err)
	}
	e.mustSend("next normal", "")
	select {
	case <-e.ex.started:
		release()
		t.Fatal("normal dispatch bypassed fork gate")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	release()
	e.waitStarted("next normal")
	e.waitIdle()
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if release, err := e.svc.PauseDispatch(cancelled, e.agent); !errors.Is(err, context.Canceled) {
		if release != nil {
			release()
		}
		t.Fatal("cancelled fork acquired gate", err)
	}
	e.mustSend("after cancellation", "")
	e.waitStarted("after cancellation")
	e.waitIdle()
}
