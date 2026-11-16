package ccstream

import (
	"sync"
	"unicode/utf8"
)

// ringBuffer keeps only the last limit bytes written, and is safe for concurrent use.
type ringBuffer struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

func newRingBuffer(limit int) *ringBuffer { return &ringBuffer{limit: limit} }

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(p)
	if n >= r.limit {
		r.buf = append(r.buf[:0], p[n-r.limit:]...)
		return n, nil
	}
	if over := len(r.buf) + n - r.limit; over > 0 {
		r.buf = append(r.buf[:0], r.buf[over:]...)
	}
	r.buf = append(r.buf, p...)
	return n, nil
}

// String returns the buffer content, dropping incomplete UTF-8 bytes at the start left by truncation.
func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.buf
	for i := 0; i < utf8.UTFMax && len(b) > 0 && !utf8.RuneStart(b[0]); i++ {
		b = b[1:]
	}
	return string(b)
}
