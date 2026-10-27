// Package identity 是外部身份的名称规则与令牌签发，控制台接口与服务端命令行共用。
package identity

import (
	"context"
	"regexp"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidateName 检查外部身份的名称，规则与契约 ExternalIdentityName 相同；满足时返回空字符串。
func ValidateName(name string) string {
	switch {
	case !namePattern.MatchString(name):
		return "只能由小写字母、数字、点、下划线和连字符组成，以小写字母或数字开头，最长 64 个字符"
	case name == "console" || name == "platform":
		return "不能是 console 或 platform"
	}
	return ""
}

// IssueToken 为未删除的外部身份签发令牌；expiresAt 不为空时必须晚于当前时间。返回令牌元数据与只出现这一次的明文。
func IssueToken(ctx context.Context, st *store.Store, identityID uuid.UUID, expiresAt *time.Time) (store.ExternalToken, string, error) {
	if expiresAt != nil && !expiresAt.After(time.Now()) {
		return store.ExternalToken{}, "", apperr.Invalid("expiresAt", "过期时间必须晚于当前时间")
	}
	tok, err := auth.NewToken(auth.ExternalPrefix)
	if err != nil {
		return store.ExternalToken{}, "", err
	}
	t, err := st.InsertExternalToken(ctx, identityID, tok.ID, tok.Hash, auth.DisplayPrefix(tok.ID), expiresAt)
	if err != nil {
		return store.ExternalToken{}, "", err
	}
	return t, tok.Plain, nil
}
