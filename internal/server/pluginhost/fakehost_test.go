package pluginhost_test

import (
	"os"
	"testing"

	"loop-now/internal/server/pluginhost/pluginhosttest"
)

const fakeHostEnv = pluginhosttest.Env

// The fake plugin host is in pluginhosttest; with LOOPNOW_FAKE_PLUGIN_HOST=1 only this test runs.
func TestFakeHostProcess(t *testing.T) {
	if os.Getenv(fakeHostEnv) != "1" {
		t.Skip("runs only as the fake plugin host process")
	}
	pluginhosttest.Run()
	os.Exit(0)
}
