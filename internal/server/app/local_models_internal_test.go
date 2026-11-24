package app

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// A read of the model lists starts one background read of Codex's list once the list is older than maxAge, or than
// staleAge when the last read could not refresh it online; the read itself does not wait.
func TestLocalModelReaderReadsAgainInTheBackground(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	reads := make(chan chan bool, 4)
	r := &localModelReader{maxAge: time.Minute, staleAge: 10 * time.Second, now: clock.Now,
		read: func(context.Context) bool {
			reply := make(chan bool)
			reads <- reply
			return <-reply
		}}
	finish := func(stale bool) {
		t.Helper()
		select {
		case reply := <-reads:
			reply <- stale
		case <-time.After(5 * time.Second):
			t.Fatal("no read started")
		}
		r.wait()
	}
	none := func(why string) {
		t.Helper()
		select {
		case <-reads:
			t.Fatal(why)
		case <-time.After(50 * time.Millisecond):
		}
	}

	started := make(chan struct{})
	go func() { r.refresh(context.Background()); close(started) }() // startup
	finish(false)
	<-started
	r.touch(context.Background())
	none("a list just read is not read again")
	clock.add(59 * time.Second)
	r.touch(context.Background())
	none("not before a minute")
	clock.add(2 * time.Second)
	r.touch(context.Background()) // returns at once; the read runs in the background
	r.touch(context.Background())
	select {
	case reply := <-reads:
		r.touch(context.Background())
		select {
		case <-reads:
			t.Fatal("only one read at a time")
		case <-time.After(50 * time.Millisecond):
		}
		reply <- true // the login was rejected: the list may be out of date
		r.wait()
	case <-time.After(5 * time.Second):
		t.Fatal("an old list is read again")
	}
	clock.add(11 * time.Second)
	r.touch(context.Background())
	finish(false) // a list that may be out of date is read again sooner
	clock.add(11 * time.Second)
	r.touch(context.Background())
	none("once it is fresh, a minute again")
}
