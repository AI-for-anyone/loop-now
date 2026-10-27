package pirpc

import (
	"sync"
	"unicode/utf8"
)

// ringBuffer 只保留最近写入的 limit 字节，并发安全。
type ringBuffer struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

func newRingBuffer(limit int) *ringBuffer {
	return &ringBuffer{limit: limit}
}

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

// String 返回缓冲内容，去掉截断造成的开头残缺 UTF-8 字节。
func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.buf
	for i := 0; i < utf8.UTFMax && len(b) > 0 && !utf8.RuneStart(b[0]); i++ {
		b = b[1:]
	}
	return string(b)
}
