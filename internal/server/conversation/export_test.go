package conversation

import "github.com/google/uuid"

// SetTestHookBeforeInterrupt 设置打断前的测试钩子；传 nil 清除。
func SetTestHookBeforeInterrupt(h func(runID uuid.UUID)) {
	if h == nil {
		testHookBeforeInterrupt.Store(nil)
		return
	}
	testHookBeforeInterrupt.Store(&h)
}

// SetTestHookBeforeMarkAborting 设置标记打断前的测试钩子；传 nil 清除。
func SetTestHookBeforeMarkAborting(h func(byRunID uuid.UUID)) {
	if h == nil {
		testHookBeforeMarkAborting.Store(nil)
		return
	}
	testHookBeforeMarkAborting.Store(&h)
}
