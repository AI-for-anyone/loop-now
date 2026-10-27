package larksidecar

import "bytes"

// exactTokenRedactor keeps at most len(secret)-1 pending bytes between reads.
// It changes only exact occurrences of this request's committed access token,
// including occurrences split across reads. It does not guess token formats or
// alter unrelated message fields. On a truncated/cancelled response pending data
// is discarded; finish is only for a confirmed EOF.
type exactTokenRedactor struct {
	secret, replacement, pending []byte
	emit                         func([]byte) error
}

// A character absent from the secret cannot recreate that secret across a
// replacement boundary (a fixed marker theoretically can). All choices are safe
// inside JSON string values. If none exists, reject this response before headers.
func tokenMask(secret []byte) ([]byte, bool) {
	if len(secret) == 0 {
		return nil, false
	}
	choices := append([]byte("*#~!"), []byte(" $%&()+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[]^_`abcdefghijklmnopqrstuvwxyz{|}")...)
	for _, c := range choices {
		if !bytes.ContainsRune(secret, rune(c)) {
			return bytes.Repeat([]byte{c}, 4), true
		}
	}
	return nil, false
}

func (r *exactTokenRedactor) write(p []byte) error {
	r.pending = append(r.pending, p...)
	for len(r.pending) >= len(r.secret) {
		i := bytes.Index(r.pending, r.secret)
		if i < 0 {
			n := len(r.pending) - len(r.secret) + 1
			if e := r.emit(r.pending[:n]); e != nil {
				return e
			}
			// Compact only the bounded lookbehind for the next read.
			r.pending = append(r.pending[:0], r.pending[n:]...)
			return nil
		}
		if i > 0 {
			if e := r.emit(r.pending[:i]); e != nil {
				return e
			}
		}
		if e := r.emit(r.replacement); e != nil {
			return e
		}
		r.pending = r.pending[i+len(r.secret):]
	}
	return nil
}

func (r *exactTokenRedactor) finish() error {
	if len(r.pending) == 0 {
		return nil
	}
	e := r.emit(r.pending)
	r.pending = nil
	return e
}
