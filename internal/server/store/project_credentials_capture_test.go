package store_test

import (
	"sync"
)

type projectCaptureGate struct {
	once             sync.Once
	entered, release chan struct{}
}
