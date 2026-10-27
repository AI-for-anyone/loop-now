package clitemplate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestInstallerCloseDrainsSharedCallAndRejectsLateAdmission(t *testing.T) {
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	f, e := zw.Create("fixture/tool")
	if e != nil {
		t.Fatal(e)
	}
	f.Write([]byte("#!/bin/sh\nexit 0\n"))
	zw.Close()
	sum := sha256.Sum256(archive.Bytes())
	entered, release := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		close(entered)
		<-release
		w.Write(archive.Bytes())
	}))
	defer server.Close()
	template := &Template{Name: "fixture", Version: "1", Binary: "fixture", Install: map[string]Asset{Platform(): {URL: server.URL, SHA256: hex.EncodeToString(sum[:]), Format: "zip", Path: "fixture/tool"}}}
	installer := NewInstaller(t.TempDir(), 2*time.Second, server.Client())
	caller, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, e := installer.Ensure(caller, template); first <- e }()
	<-entered
	// Cancelling one consumer does not abort the platform-owned installation.
	cancel()
	closing := make(chan error, 1)
	go func() { closing <- installer.Close(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for {
		installer.mu.Lock()
		closed := installer.closed
		installer.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("close admission was not published")
		}
		time.Sleep(time.Millisecond)
	}
	if _, e = installer.Ensure(context.Background(), template); e == nil {
		t.Fatal("late Ensure entered after shutdown")
	}
	select {
	case e := <-closing:
		t.Fatal("drain returned while shared install was active", e)
	default:
	}
	close(release)
	if e = <-first; e != nil {
		t.Fatal("consumer cancel aborted shared install", e)
	}
	if e = <-closing; e != nil {
		t.Fatal(e)
	}
	if requests.Load() != 1 {
		t.Fatal("late admission dispatched another download")
	}
	if !installer.installed(template) {
		t.Fatal("shared publication was removed")
	}
}
func TestInstallerCloseTimeoutIsIncompleteAndLateGoroutineCannotStart(t *testing.T) {
	installer := NewInstaller(t.TempDir(), time.Second, nil)
	call := &installCall{done: make(chan struct{})}
	installer.mu.Lock()
	installer.inflight["held"] = call
	installer.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if e := installer.Close(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("incomplete drain was reported successful", e)
	}
	catalog, e := Load("")
	if e != nil {
		t.Fatal(e)
	}
	template, _ := catalog.Get("gh")
	start := make(chan struct{})
	done := make(chan error, 1)
	go func() { <-start; _, e := installer.Ensure(context.Background(), template); done <- e }()
	close(start)
	if e = <-done; e == nil {
		t.Fatal("previously scheduled goroutine started after close")
	}
	close(call.done)
	if e = installer.Close(context.Background()); e != nil {
		t.Fatal("completed drain could not be confirmed", e)
	}
}
