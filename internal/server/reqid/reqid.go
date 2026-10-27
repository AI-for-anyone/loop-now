// Package reqid 在 context 中传递接口请求编号，供日志与运行记录关联。
package reqid

import "context"

type key struct{}

// With 返回带请求编号的 context。
func With(ctx context.Context, id string) context.Context { return context.WithValue(ctx, key{}, id) }

// From 返回 context 中的请求编号；没有时返回空串。
func From(ctx context.Context) string {
	id, _ := ctx.Value(key{}).(string)
	return id
}
