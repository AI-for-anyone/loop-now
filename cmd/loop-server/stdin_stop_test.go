package main

import (
	"context"
	"io"
	"testing"
	"time"
)

// Only closing standard input cancels: content written before does not trigger it; after the write end is closed,
// ctx ends and the callback runs exactly once.
func TestStopOnStdinClose(t *testing.T) {
	pr, pw := io.Pipe()
	calls := 0
	ctx := stopOnStdinClose(context.Background(), pr, func() { calls++ })
	if _, err := pw.Write([]byte("noise\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("写入不应触发退出")
	case <-time.After(50 * time.Millisecond):
	}
	_ = pw.Close()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("关闭标准输入后应取消")
	}
	if calls != 1 {
		t.Fatalf("回调应恰好一次：%d", calls)
	}
}

// When the parent ctx ends first, "stdin closed" is no longer reported.
func TestStopOnStdinCloseParentFirst(t *testing.T) {
	pr, pw := io.Pipe()
	parent, cancel := context.WithCancel(context.Background())
	called := make(chan struct{}, 1)
	ctx := stopOnStdinClose(parent, pr, func() { called <- struct{}{} })
	cancel()
	<-ctx.Done()
	_ = pw.Close()
	select {
	case <-called:
		t.Fatal("父 ctx 已结束时不应再报告 stdin 关闭")
	case <-time.After(100 * time.Millisecond):
	}
}
