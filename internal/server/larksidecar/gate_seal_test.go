package larksidecar

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestConcurrentSealWaitHonorsOwnDeadline(t *testing.T) {
	// This focused gate fixture uses a real dual-listener broker and an owned
	// idle TCP connection. Its final ConnState notification is held at a barrier,
	// so the first real Seal reaches Close/Wait but cannot yet prove drain. It
	// does not claim native or OpenScope admission coverage (covered separately).
	closedAtBarrier, finish := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(finish) }) }
	defer unblock()
	ln, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal("owned IPv4 listener")
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ln6, e := net.Listen("tcp6", net.JoinHostPort("::1", port))
	if e != nil {
		ln.Close()
		t.Fatal("owned IPv6 listener")
	}
	zero := make(chan struct{})
	close(zero)
	_, stop := context.WithCancel(context.Background())
	b := &Broker{address: "http://" + ln.Addr().String(), address6: ln6.Addr().String(), cancel: stop, client: &http.Client{Transport: &http.Transport{}}, idle: zero, connectionsIdle: zero, serveDone: make(chan struct{})}
	// Install the test barrier BEFORE starting either Serve goroutine. Replacing
	// an already-running http.Server.ConnState is itself a test data race.
	b.server = &http.Server{Handler: b, ConnState: func(c net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closedAtBarrier <- struct{}{}
			<-finish
		}
		b.connectionState(c, state)
	}}
	go func() {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _ = b.server.Serve(ln) }()
		go func() { defer wg.Done(); _ = b.server.Serve(ln6) }()
		wg.Wait()
		close(b.serveDone)
	}()
	t.Cleanup(func() {
		unblock()
		_ = b.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if b.Wait(ctx) != nil {
			t.Error("owned broker did not drain")
		}
	})
	c, e := net.DialTimeout("tcp4", strings.TrimPrefix(b.URL(), "http://"), time.Second)
	if e != nil {
		t.Fatal("owned connection")
	}
	defer c.Close()
	until := time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		n := b.connections
		b.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("owned connection not admitted")
		}
		time.Sleep(time.Millisecond)
	}
	m := NewManager()
	id := uuid.New()
	absent := uuid.New()
	s := &ManagedScope{manager: m, broker: b, id: id.String(), cancel: func() {}, idle: zero}
	m.scopes[s.id] = s
	g, e := m.AcquireRuntimeGate(context.Background(), []uuid.UUID{id, absent})
	if e != nil {
		t.Fatal("idle gate acquisition")
	}
	firstCtx, cancelFirst := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelFirst()
	firstDone := make(chan error, 1)
	go func() { firstDone <- g.Seal(firstCtx) }()
	select {
	case <-closedAtBarrier:
	case <-time.After(time.Second):
		t.Fatal("first Seal did not reach actual close/drain barrier")
	}
	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelSecond()
	start := time.Now()
	e = g.Seal(secondCtx)
	if !errors.Is(e, context.DeadlineExceeded) || time.Since(start) > 500*time.Millisecond {
		t.Fatal("concurrent Seal ignored its own deadline")
	}
	select {
	case <-firstDone:
		t.Fatal("first Seal reported drain before real connection exit")
	default:
	}
	g.Release() // Must retain the gate while first Seal is not sealed-ready.
	if other, e := m.AcquireRuntimeGate(context.Background(), []uuid.UUID{id, absent}); !errors.Is(e, ErrNativeBusy) || other != nil {
		t.Fatal("timed-out waiter released unconfirmed gate")
	}
	unblock()
	select {
	case e := <-firstDone:
		if e != nil {
			t.Fatal("first Seal did not finish after real drain")
		}
	case <-time.After(time.Second):
		t.Fatal("first Seal remained blocked after drain")
	}
	g.Release()
	g.Release()
	other, e := m.AcquireRuntimeGate(context.Background(), []uuid.UUID{id, absent})
	if e != nil {
		t.Fatal("sealed gate release did not finish")
	}
	if other.Seal(context.Background()) != nil {
		t.Fatal("empty reacquired pair did not seal")
	}
	other.Release()
}
