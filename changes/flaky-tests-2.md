## English

### Quitting Loop Now waits for an Agent's CLI that is still being wound down

- When an Agent's CLI process had just exited by itself, or was ending because its reply was stopped, quitting the server could finish while Loop Now was still writing that process's last records into the Agent's folder. Quitting now waits for that, within its usual time limit.

## 中文

### 退出 Loop Now 时，会等正在收尾的 Agent CLI

- 如果某个 Agent 的 CLI 进程刚好自己退出，或者因为回复被停止正在结束，退出服务器时可能在 Loop Now 还往这个 Agent 的文件夹里写最后几样记录时就结束了。现在退出会等这一步做完，最多等到原有的时限。

## MAIN.md

- Two more server tests that failed CI at random now pass every time (2026-11-26; each failure cost a pull request another 7-minute CI run).
  - `TestResetSwitchDeleteShutdownStopTasks` and `TestServiceTierExecutorRebuildsAndResumesFixedParameter` ("TempDir RemoveAll cleanup: … agents/<id>: directory not empty") (code): a process taken out of the process table and stopped elsewhere (exited by itself and wrapped up by `watch`, or retired after its reply was stopped) was not waited for by `Executor.Shutdown`, which could return while that wrap-up still wrote in the Agent's folder (the command list save from #109 among it). Shutdown now also waits, within its context, for every process in `e.live` to be stopped. New test `TestShutdownWaitsForProcessesStoppedElsewhere` holds the wrap-up of both kinds and fails every time without the fix. The CI failures of `/delete` and of the service tier test predate #109's command list fix, which closes them.
  - `TestHostLoadsDeliversEventsAndKeepsState` ("timed out waiting for loading and delivery: running <nil>"): its events were dated 2026-10-27 08:00 UTC, so from 2026-11-26 08:00 UTC the host pruned them (EventRetention 30 days) before delivery. Already fixed on main by fe213ee (events an hour before the test runs); every CI failure (08:47–09:13 UTC) predates it. No change here.
  - Stress (race build, 8 shards at GOMAXPROCS=1 next to 8 busy loops): Shutdown tests 0/3200 after the fix, 33/3200 with it reverted (32 `reply_stopped`, 1 `exit`); with the command list save delayed 50 ms by an injected probe, 0/400 after, 400/400 before; the new test 0/800 after. pluginhost test 0/200 now, 8/8 with fe213ee's old date put back.
