package main

import (
	"context"
	"io"
)

// stopOnStdinClose returns a ctx that is canceled when reading r hits EOF (or an error): whatever is read is
// discarded, and onClose is called once before the cancel.
// It also ends when the parent ctx ends (the reader goroutine goes away when the process exits; there is no separate
// cleanup).
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
