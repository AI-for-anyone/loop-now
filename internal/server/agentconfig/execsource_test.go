package agentconfig_test

import (
	"sync"
)

type pauseExecutionRead struct {
	once             sync.Once
	entered, release chan struct{}
}
