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

// SetTestHookAfterDriveHandoff 设置空队列交接后的测试钩子；传 nil 清除。
func SetTestHookAfterDriveHandoff(h func(agentID uuid.UUID)) {
	if h == nil {
		testHookAfterDriveHandoff.Store(nil)
		return
	}
	testHookAfterDriveHandoff.Store(&h)
}

// SetTestHookAfterDriverPending 设置 pendingDrivers 登记后的测试钩子；传 nil 清除。
func SetTestHookAfterDriverPending(h func(runID uuid.UUID)) {
	if h == nil {
		testHookAfterDriverPending.Store(nil)
		return
	}
	testHookAfterDriverPending.Store(&h)
}
