package larkauth

import (
	"context"
	"loop-now/internal/server/clicredential"
)

// TokenSource is created by trusted scope preparation. A CLI request cannot
// supply a grant, Agent, project or account to this interface.
type TokenSource struct {
	coordinator *Coordinator
	binding     clicredential.LarkBinding
}

func (c *Coordinator) TokenSource(b clicredential.LarkBinding) (*TokenSource, error) {
	if c == nil || !b.Valid() {
		return nil, ErrUnavailable
	}
	return &TokenSource{c, b}, nil
}
func (s *TokenSource) AccessToken(ctx context.Context) (string, error) {
	if s == nil || s.coordinator == nil {
		return "", ErrUnavailable
	}
	credential, e := s.coordinator.Resolve(ctx, s.binding)
	if e != nil {
		return "", e
	}
	return credential.AccessToken, nil
}
func (*TokenSource) String() string   { return "[bound Lark token source]" }
func (*TokenSource) GoString() string { return "[bound Lark token source]" }
