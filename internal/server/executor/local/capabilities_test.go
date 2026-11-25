package local

import (
	"testing"

	"loop-now/internal/server/agentruntime"
)

// The registry's capabilities match the drivers: every runtime has a driver with the same id and name, and each
// capability that has an optional driver interface is on exactly when the driver implements it.
func TestCapabilitiesMatchDrivers(t *testing.T) {
	e := &Executor{}
	for _, rt := range agentruntime.All() {
		drv := e.driverByID(rt.ID)
		if drv == nil {
			t.Fatalf("%s has no driver", rt.ID)
		}
		if drv.runtime() != rt.ID || drv.name() != rt.Name {
			t.Errorf("%s: driver says %s / %s", rt.ID, drv.runtime(), drv.name())
		}
		_, forker := drv.(taskForker)
		_, compacts := drv.(compactor)
		_, exports := drv.(sessionExporter)
		_, lists := drv.(sessionLister)
		_, commands := drv.(nativeCommander)
		_, writes := drv.(textWriter)
		acp, isACP := drv.(acpDriver)
		// A stop ends the background tasks when it ends the process (Grok), or when its session/cancel ends the session's
		// background commands (Antigravity).
		if ends := isACP && (acp.f.killOnStop || acp.f.background != nil); rt.Capabilities.StopEndsBackgroundTasks != ends {
			t.Errorf("%s: stopEndsBackgroundTasks is %v, but a stop ends them: %v", rt.ID, rt.Capabilities.StopEndsBackgroundTasks, ends)
		}
		if (rt.TextGeneration != nil) != writes {
			t.Errorf("%s: writes texts in the registry %v, the driver implements it: %v", rt.ID, rt.TextGeneration != nil, writes)
		}
		caps := rt.Capabilities
		for name, pair := range map[string][2]bool{
			"taskFork":                {caps.TaskFork, forker},
			"sessionCommands compact": {caps.HasSessionCommand(agentruntime.CommandCompact), compacts},
			"sessionCommands export":  {caps.HasSessionCommand(agentruntime.CommandExport), exports},
			"sessionCommands resume":  {caps.HasSessionCommand(agentruntime.CommandResume), lists},
			"nativeCommands":          {caps.NativeCommands, commands},
		} {
			if pair[0] != pair[1] {
				t.Errorf("%s: capability %s is %v but the driver implements it: %v", rt.ID, name, pair[0], pair[1])
			}
		}
	}
	for id := range drivers {
		if !agentruntime.Known(id) {
			t.Errorf("a driver for the unknown runtime %s", id)
		}
	}
}
