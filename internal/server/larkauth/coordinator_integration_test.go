package larkauth

import (
	"context"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/store"
)

type larkFixture struct {
	st         *store.Store
	binding    clicredential.LarkBinding
	credential clicredential.Lark
}

type larkRefreshFunc func(context.Context, clicredential.Lark) (clicredential.Lark, error)

func (f larkRefreshFunc) Refresh(ctx context.Context, c clicredential.Lark) (clicredential.Lark, error) {
	return f(ctx, c)
}
