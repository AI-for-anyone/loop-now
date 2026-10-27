package larksidecar

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func lifecycleBroker(t *testing.T, source TokenSource) *Broker {
	t.Helper()
	b, e := Start(context.Background(), testBinding(), source, Limits{RequestTimeout: 2 * time.Second})
	if e != nil {
		t.Fatal("isolated broker start")
	}
	t.Cleanup(func() {
		_ = b.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if b.Wait(ctx) != nil {
			t.Error("owned broker did not drain")
		}
	})
	return b
}

func TestBrokerFreezeIdleAdmissionAndRelease(t *testing.T) {
	var calls atomic.Int32
	b := lifecycleBroker(t, TokenSourceFunc(func(context.Context) (string, error) {
		calls.Add(1)
		return "", errors.New("synthetic unavailable")
	}))
	if !errors.Is(b.Wait(context.Background()), ErrBrokerNotClosed) {
		t.Fatal("Wait before Close reported drain")
	}
	release, e := b.FreezeIdle()
	if e != nil || release == nil {
		t.Fatal("idle freeze failed")
	}
	if again, e := b.FreezeIdle(); !errors.Is(e, ErrBrokerBusy) || again != nil {
		t.Fatal("second freeze acquired another lease")
	}
	if status(t, signed(b, "GET", "/open-apis/authen/v1/user_info", nil)) != 503 || calls.Load() != 0 {
		t.Fatal("frozen broker admitted request")
	}
	release()
	release() // Must not affect a later lease.
	if status(t, signed(b, "GET", "/open-apis/authen/v1/user_info", nil)) != 503 || calls.Load() != 1 {
		t.Fatal("release did not restore admission")
	}
	lease2, e := b.FreezeIdle()
	if e != nil {
		t.Fatal("second idle lease failed")
	}
	release()
	if status(t, signed(b, "GET", "/open-apis/authen/v1/user_info", nil)) != 503 || calls.Load() != 1 {
		t.Fatal("stale release unfroze later lease")
	}
	_ = b.Close()
	lease2()
	if next, e := b.FreezeIdle(); !errors.Is(e, ErrBrokerClosed) || next != nil {
		t.Fatal("closed broker reopened")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if b.Wait(ctx) != nil {
		t.Fatal("idle close did not drain connections")
	}
}

func TestBrokerBusyDoesNotCancelAndWaitProvesHandlerExit(t *testing.T) {
	entered := make(chan context.Context, 1)
	finish := make(chan struct{})
	var finishOnce sync.Once
	unblock := func() { finishOnce.Do(func() { close(finish) }) }
	defer unblock()
	b := lifecycleBroker(t, TokenSourceFunc(func(ctx context.Context) (string, error) {
		entered <- ctx
		<-finish // Deliberately ignore cancellation: Close is not a join.
		return "", errors.New("synthetic unavailable")
	}))
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		client := &http.Client{Timeout: 2 * time.Second}
		defer client.CloseIdleConnections()
		resp, e := client.Do(signed(b, "GET", "/open-apis/authen/v1/user_info", nil))
		if e == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	var requestCtx context.Context
	select {
	case requestCtx = <-entered:
	case <-time.After(time.Second):
		t.Fatal("owned request did not enter source")
	}
	if release, e := b.FreezeIdle(); !errors.Is(e, ErrBrokerBusy) || release != nil {
		t.Fatal("in-flight handler incorrectly froze")
	}
	select {
	case <-requestCtx.Done():
		t.Fatal("busy freeze cancelled in-flight request")
	default:
	}
	if b.Close() != nil || b.Close() != nil {
		t.Fatal("idempotent Close failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if !errors.Is(b.Wait(ctx), context.DeadlineExceeded) {
		t.Fatal("Close/listener exit mistaken for handler drain")
	}
	unblock()
	select {
	case <-clientDone:
	case <-time.After(time.Second):
		t.Fatal("owned client did not finish")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if b.Wait(ctx2) != nil {
		t.Fatal("handler did not drain after actual return")
	}
	b.mu.Lock()
	active, connections := b.active, b.connections
	b.mu.Unlock()
	if active != 0 || connections != 0 {
		t.Fatal("Wait returned before handlers/connections drained")
	}
}

func TestBrokerFreezeAtomicWithHandlerAdmission(t *testing.T) {
	// Repeatedly race the two real entry points. The source cannot exit until
	// released, so a successful freeze must exclude it, while an admitted source
	// must cause busy. No len(slots) snapshot or check/increment window is used.
	for i := 0; i < 100; i++ {
		entered := make(chan struct{}, 1)
		finish := make(chan struct{})
		b := lifecycleBroker(t, TokenSourceFunc(func(context.Context) (string, error) {
			entered <- struct{}{}
			<-finish
			return "", errors.New("synthetic unavailable")
		}))
		start, handlerDone := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(handlerDone)
			<-start
			b.ServeHTTP(httptest.NewRecorder(), signed(b, "GET", "/open-apis/authen/v1/user_info", nil))
		}()
		close(start)
		release, e := b.FreezeIdle()
		if e == nil {
			select {
			case <-handlerDone:
			case <-time.After(time.Second):
				close(finish)
				t.Fatal("frozen handler did not reject")
			}
			select {
			case <-entered:
				close(finish)
				t.Fatal("successful freeze raced past active admission")
			default:
			}
			close(finish)
			release()
		} else {
			if !errors.Is(e, ErrBrokerBusy) || release != nil {
				close(finish)
				t.Fatal("unexpected freeze result")
			}
			select {
			case <-entered:
			case <-time.After(time.Second):
				close(finish)
				t.Fatal("busy result without admitted handler")
			}
			close(finish)
			<-handlerDone
		}
		_ = b.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		e = b.Wait(ctx)
		cancel()
		if e != nil {
			t.Fatal("race iteration did not drain")
		}
	}
}
