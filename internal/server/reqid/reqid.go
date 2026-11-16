// Package reqid carries the API request ID in the context, so logs and run records can be correlated with it.
package reqid

import "context"

type key struct{}

// With returns a context carrying the request ID.
func With(ctx context.Context, id string) context.Context { return context.WithValue(ctx, key{}, id) }

// From returns the request ID in the context; an empty string when there is none.
func From(ctx context.Context) string {
	id, _ := ctx.Value(key{}).(string)
	return id
}
