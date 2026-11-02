package httpapi_test

import (
	"sync"
)

type pauseTaskPrompt struct {
	mu               sync.Mutex
	count            int
	entered, release chan struct{}
}
