package cliauth

import (
	"context"

	"github.com/google/uuid"
	"loop-now/internal/server/clicredential"
)

type offlineBrowser struct {
	helper *ProcessHelper
	onURL  func(string) error
}

func (h offlineBrowser) Login(ctx context.Context, id uuid.UUID, save func(string) error) (clicredential.Google, error) {
	return h.helper.Login(ctx, id, func(u string) error {
		if e := save(u); e != nil {
			return e
		}
		return h.onURL(u)
	})
}
