# fakepi

[中文](README.zh-CN.md)

A test double that imitates the part of the Pi CLI/RPC the platform uses (written from the Pi 1.1.0 source). It never calls a model.

```bash
go build -o /tmp/fakepi ./internal/server/executor/local/fakepi
/tmp/fakepi --version                      # prints $FAKEPI_VERSION, 1.4.0 by default (the version the platform pins)
/tmp/fakepi --mode rpc --session-dir /tmp/s  # RPC mode, JSONL on stdin/stdout
```

## Arguments

It accepts `--mode rpc`, `--no-approve`, `--offline`, `--no-skills`, `--skill <p>` (repeatable), `-e <p>` (repeatable),
`--append-system-prompt <p>`, `--provider <p>`, `--model <m>`, `--session-dir <d>`,
`--session <file>` and `--no-session`; other arguments are ignored.

- `FAKEPI_DUMP=<path>`: at startup, writes `{"args":[...],"env":{...},"cwd":"..."}` to that file (args without the program name).
- `FAKEPI_NEUTRAL=1`: neutral replies (for screenshots). The default reply becomes `Got it: <message>`: the run ID line the platform adds (`Run ID for this turn: …`, and `本轮运行ID：…` in old sessions) and the directive words are removed,
  newlines are kept, and when nothing is left the reply is `Got it.`; `!text` still wins. The extension dialog title, the compaction summary, the retryable error and the stderr line on a crash are English in both modes.
  Without it the behaviour is unchanged. The server passes only allowlisted variables to Pi, so when Pi is started through the server, also add it to
  `LOOP_NOW_EXEC_ENV_PASSTHROUGH` (for example `LANG,LC_ALL,TZ,FAKEPI_NEUTRAL`).
- `--session <file>`: when the file does not exist, writes an error to stderr and exits with 1 (note: the real Pi 1.1.0 creates a new session at that path
  for a path-style `--session` and does not fail).
- Without `--session`, the session file is `<session-dir>/<timestamp>_<id>.jsonl`, created when the first prompt arrives;
  the first line is the session header, then each user, assistant and tool result message is appended as a line.

## Commands

`get_state`, `get_commands`, `prompt`, `abort`, `clear_queue` and `get_session_stats`; other commands return `success:false`.
`get_commands` lists commands from the startup arguments: one extension command per `-e <file>` (except `builtin:`), named after the file without its extension, and one `skill:<directory name>` per `--skill <directory>`.
As in Pi, a prompt whose first character is `/` and whose command name is one of the extension commands is handled by the extension directly: the response has `disposition: "handled"` and the turn has no events.
`get_session_stats` returns, in Pi 1.1.0's `SessionStats` shape, the sum of all usage this process has reported (including tool results, compaction and cache warming); `cost` is always 0.
A `prompt` that arrives while a turn is running returns `success:false` (as in Pi). During a turn, `abort` first emits
`message_end` (stopReason `aborted`, with the text output so far) and `agent_settled{aborted:true}`, then returns its response;
when idle it succeeds at once. When stdin ends, it exits with 0.

## Prompt directives

Words in the prompt text that start with `!`, separated by whitespace. The default reply is `echo: <message>` (without the directive words when there are any), streamed in 3 parts.

| Directive | Behaviour |
|---|---|
| `!text=<s>` | Reply text; `_` stands for a space |
| `!tool=<name>` | Run one tool call before replying (repeatable) |
| `!toolfail=<name>` | Same, but with `isError: true` |
| `!sleep=<duration>` | Sleep between text parts (a Go duration); abort interrupts it |
| `!ask=<method>` | Before replying, send an `extension_ui_request` (confirm, select or input; select's options are staging and production), wait for the `extension_ui_response`, then reply `answer confirmed=…`, `answer value=…` or `answer cancelled`; abort interrupts it |
| `!error=<msg>` | The final assistant message has stopReason `error` and errorMessage msg (`_` stands for a space); without `!text` no text is output |
| `!retry` | First output an assistant message with stopReason `error`, then the successful reply (imitates an automatic retry) |
| `!crash` | Exit with 3 after the first text part of the reply |
| `!noabort` | Ignore abort: never respond and keep sleeping |
| `!rejectprompt` | The prompt response is `success:false` with error `rejected` |
| `!handled` | The prompt response is `data: {"disposition":"handled"}` and no events are emitted |
| `!nosession` | Do not write the session file this turn (imitates a first turn that fails before it is saved) |
| `!usage=I,O[,CR[,CW[,R]]]` | Every finished assistant message this turn carries this usage (input, output, cache read, cache write, reasoning); all 0 by default |
| `!respmodel=<name>` | Assistant messages carry `responseModel` |
| `!toolusage=I,O[,CR[,CW]]` | Tool result messages carry this usage |
| `!compact=I,O[,CR[,CW]]` | Before the final reply, output `compaction_start`/`compaction_end` with this usage in `result.usage` |
| `!warm=I,O[,CR[,CW]]` | Before the final reply, output a cache-warming usage entry (`entry_appended` with `kind` `cache_warm`) |
| `!warmafter=I,O[,CR[,CW]]` | About 50ms after `agent_settled`, output a cache-warming usage entry (between turns) |

Every assistant message ends with `durationMs`; before exiting, `!crash` writes the line `fakepi: simulated crash` to stderr.
