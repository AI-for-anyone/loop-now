package api_test

import (
	"slices"
	"testing"

	"loop-now/internal/server/contracttest"
	"loop-now/internal/server/executor/local"
)

// 执行器可能记下的每一个 pi.stopped 原因都要在 ProcessExit.reason 与 TimelinePiStopped.reason 的枚举里。
func TestProcessStopReasonsInContract(t *testing.T) {
	console, agent := contracttest.LoadFile(t, "openapi.yaml"), contracttest.LoadFile(t, "agent-openapi.yaml")
	for _, c := range []struct{ file, schema string }{{"openapi.yaml", "ProcessExit"}, {"openapi.yaml", "TimelinePiStopped"}, {"agent-openapi.yaml", "TimelinePiStopped"}} {
		doc := console
		if c.file == "agent-openapi.yaml" {
			doc = agent
		}
		enum := doc.Components.Schemas[c.schema].Value.Properties["reason"].Value.Enum
		for _, reason := range local.StopReasons {
			if !slices.Contains(enum, any(reason)) {
				t.Errorf("%s %s.reason 缺少 %s", c.file, c.schema, reason)
			}
		}
	}
}
