package larksidecar

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
)

var ErrBrokerBusy = errors.New("lark broker busy")
var ErrBrokerClosed = errors.New("lark broker closed")
var ErrBrokerNotClosed = errors.New("lark broker not closed")

// FreezeIdle atomically excludes new admitted handlers and confirms none is
// active. It does not cancel or wait out an in-flight request to manufacture idle.
// Freeze/admission/counter changes share mu; a slots length check is insufficient.
// The caller freezes the entire runtime group before stopping any member.
// This counts broker handlers, NOT native consumers; the exec manager must retain
// its separate admission-to-Wait consumer gate across long CLI request gaps.
func (b *Broker) FreezeIdle() (release func(), err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrBrokerClosed
	}
	if b.frozen || b.active != 0 {
		return nil, ErrBrokerBusy
	}
	b.frozen = true
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			if !b.closed {
				b.frozen = false
			}
			b.mu.Unlock()
		})
	}, nil
}

func (b *Broker) handlerDone() {
	b.mu.Lock()
	defer b.mu.Unlock()
	<-b.slots
	b.active--
	if b.active == 0 {
		close(b.idle)
	}
}

// Track HTTP connection goroutines as well as admitted handlers. A request
// rejected after freezing may still be writing its fixed 503 response. Closed
// connection state occurs after ServeHTTP returns, so Close alone is not proof
// that these goroutines (or an uncooperative TokenSource) have drained.
func (b *Broker) connectionState(_ net.Conn, state http.ConnState) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch state {
	case http.StateNew:
		if b.connections == 0 {
			b.connectionsIdle = make(chan struct{})
		}
		b.connections++
	case http.StateClosed, http.StateHijacked:
		b.connections--
		if b.connections == 0 {
			close(b.connectionsIdle)
		}
	}
}

// Wait is called after Close and proves listener exit AND HTTP connection/
// admitted-handler drain. A deadline returns ctx.Err(), never a false success.
// Close cancels request contexts and closes connections but cannot force an
// arbitrary TokenSource to honor cancellation; Wait reports that boundary.
func (b *Broker) Wait(ctx context.Context) error {
	if ctx == nil {
		return errors.New("invalid lark broker wait")
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	b.mu.Lock()
	closed, done := b.closed, b.serveDone
	b.mu.Unlock()
	if !closed || done == nil {
		return ErrBrokerNotClosed
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
	}
	// Serve has exited, so no further accepted connection can enter StateNew;
	// closed also forbids any further admitted handler, including direct calls.
	b.mu.Lock()
	idle, connectionsIdle := b.idle, b.connectionsIdle
	b.mu.Unlock()
	for _, ch := range []chan struct{}{idle, connectionsIdle} {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		}
	}
	return ctx.Err()
}
