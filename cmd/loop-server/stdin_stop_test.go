package main

import (
	"context"
	"io"
	"testing"
	"time"
)

// 标准输入关闭才取消：之前写进来的内容不触发，关掉写端后 ctx 结束且回调恰好一次。
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

// 父 ctx 先结束时不再报告"stdin 关闭"。
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
