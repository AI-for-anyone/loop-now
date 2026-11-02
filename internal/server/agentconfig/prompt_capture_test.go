package agentconfig_test

import (
	"sync"
)

type pausePromptCapture struct {
	once             sync.Once
	entered, release chan struct{}
}
