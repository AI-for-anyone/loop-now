package agentconfig

import (
	"context"
	"github.com/google/uuid"
)

// Called only following a successful Store deletion. Failure cannot roll back
// that commit; report a safe code while normal post-commit release continues.
func (s *Service) cancelDeletedAgentLogins(ctx context.Context, ids []uuid.UUID) {
	if s.ModelAuth == nil {
		return
	}
	if err := s.ModelAuth.CancelAgentLogins(ctx, ids); err != nil && s.Logger != nil {
		s.Logger.Warn("已删除Agent的登录资源清理未及时结束", "code", "auth_cleanup_timeout", "agentIds", ids)
	}
}
