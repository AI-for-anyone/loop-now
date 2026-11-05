package main

import (
	"context"
	"io"
)

// stopOnStdinClose 返回一个在 r 读到 EOF（或出错）时取消的 ctx：读到的内容一律丢弃，onClose 在取消前调用一次。
// 父 ctx 结束时同样结束（读协程在进程退出时随之消失，不另外收尾）。
func stopOnStdinClose(parent context.Context, r io.Reader, onClose func()) context.Context {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		_, _ = io.Copy(io.Discard, r)
		if parent.Err() == nil {
			onClose()
		}
		cancel()
	}()
	return ctx
}
