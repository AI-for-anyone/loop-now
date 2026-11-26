## English

### A message sent as Claude Code starts a turn of its own gets its own reply

- A message sent at the very moment Claude Code begins a turn by itself (after background work ended) could get that turn's text as its reply, and the turn was recorded empty. The message now waits for that turn and gets its own reply.

## 中文

### Claude Code 自己开始一轮时发出的消息，拿到的是自己的回复

- 在 Claude Code 自己开始一轮（后台工作结束后）的那一刻发出的消息，可能把那一轮的文字当成自己的回复，那一轮则被记成空的。现在消息会等那一轮结束，再拿到自己的回复。

## MAIN.md

- Four server tests that failed CI at random now pass every time (2026-11-26; each failure cost a pull request another 7-minute CI run and sometimes its place in the merge queue).
  - `TestTaskStepPermissionRequestOnNode` (test only): it answered the request before the request's "created" event was emitted; it now waits for that event, as the console does.
  - `TestRecordsRetry` (code): after Pi starts, a goroutine saves its command list into the Agent's folder and nothing waited for it, so it could write after the process stopped and after Shutdown, while the folder was being removed. Stopping a process now waits for it once the process has ended.
  - `TestPromptWaitsForSelfTurn` (code): a message sent while the client was still recording a turn Claude Code began by itself was written and took that turn's output as its reply. It now waits, as it does for an open self turn.
  - `TestV1SignatureScopeReplay` (test only): its "replay" signed the write again, which is a new write when the two signings fall in different seconds; it now sends the same signed request again.
  - Verified by reproducing each failure (stress runs at GOMAXPROCS=1 next to busy loops, or by injecting the delay the cause suggests) and showing 0 failures in the same runs after the fix.
  - Checked again by a second session that wrote none of it: stress after the fix 0/1000 records, 0/400 new Pi commands test, 0/1000 self turn, 0/2000 lark, 0/200 permission; with the fixes reverted the new ccstream test failed 5/5 and the new Pi commands test failed under stress.
