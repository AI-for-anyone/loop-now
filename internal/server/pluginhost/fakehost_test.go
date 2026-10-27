package pluginhost_test

import (
	"os"
	"testing"

	"loop-now/internal/server/pluginhost/pluginhosttest"
)

const fakeHostEnv = pluginhosttest.Env

// 假宿主见 pluginhosttest；LOOPNOW_FAKE_PLUGIN_HOST=1 时只跑这个测试。
func TestFakeHostProcess(t *testing.T) {
	if os.Getenv(fakeHostEnv) != "1" {
		t.Skip("只在作为假宿主进程运行时执行")
	}
	pluginhosttest.Run()
	os.Exit(0)
}
