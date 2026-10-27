package conversation

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// PauseDispatch 等当前一轮完全退出后占用派发门禁。取消等待不会阻塞旧轮或泄漏门禁。
// 返回的release可重复调用；门禁期间队列与锁续期仍由原驱动循环管理。
func (s *Service) PauseDispatch(ctx context.Context, agentID uuid.UUID) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v, _ := s.dispatchGates.LoadOrStore(agentID, make(chan struct{}, 1))
	gate := v.(chan struct{})
	select {
	case gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate
			return nil, err
		}
		var once sync.Once
		return func() { once.Do(func() { <-gate }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
