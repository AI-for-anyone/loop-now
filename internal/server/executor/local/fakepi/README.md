# fakepi

测试替身：模拟平台用到的 Pi 1.1.0 CLI/RPC 子集，不调用任何模型。

```bash
go build -o /tmp/fakepi ./internal/server/executor/local/fakepi
/tmp/fakepi --version                      # 输出 $FAKEPI_VERSION，默认 1.1.0
/tmp/fakepi --mode rpc --session-dir /tmp/s  # RPC 模式，stdin/stdout 为 JSONL
```

## 参数

接受 `--mode rpc`、`--no-approve`、`--offline`、`--no-skills`、`--skill <p>`（可重复）、
`--append-system-prompt <p>`、`--provider <p>`、`--model <m>`、`--session-dir <d>`、
`--session <file>`、`--no-session`；其他参数忽略。

- `FAKEPI_DUMP=<path>`：启动时把 `{"args":[...],"env":{...},"cwd":"..."}` 写入该文件（args 不含程序名）。
- `--session <file>`：文件不存在时向 stderr 输出错误并以 1 退出（注意：真实 Pi 1.1.0 对路径形式的
  `--session` 会在该路径新建会话，不会报错）。
- 未指定 `--session` 时，会话文件为 `<session-dir>/<时间戳>_<id>.jsonl`，收到第一条 prompt 时才创建；
  首行为会话头，之后每条用户、助手、工具结果消息追加一行。

## 命令

`get_state`、`prompt`、`abort`、`clear_queue`、`get_session_stats`；其他命令返回 `success:false`。
`get_session_stats` 按 Pi 1.1.0 的 `SessionStats` 结构返回本进程报告过的全部用量之和（含工具结果、压缩与缓存预热），`cost` 恒为 0。
运行中再收到 `prompt` 返回 `success:false`（与 Pi 相同）。`abort` 在运行中先发出
`message_end`（stopReason `aborted`，含已输出文本）与 `agent_settled{aborted:true}`，再返回响应；
空闲时立即成功。stdin 结束即以 0 退出。

## prompt 指令

prompt 文本中以 `!` 开头、以空白分隔的词。默认回复 `echo: <消息>`（有指令时去掉指令词），分 3 段流式输出。

| 指令 | 行为 |
|---|---|
| `!text=<s>` | 回复文本，`_` 代表空格 |
| `!tool=<name>` | 回复前先执行一次工具调用（可重复） |
| `!toolfail=<name>` | 同上，但 `isError: true` |
| `!sleep=<duration>` | 文本分段之间休眠（Go duration），可被 abort 打断 |
| `!ask=<method>` | 回复前发一个 `extension_ui_request`（confirm、select、input；select 的选项为 staging、production），等到 `extension_ui_response` 后回复 `answer confirmed=…`、`answer value=…` 或 `answer cancelled`；可被 abort 打断 |
| `!error=<msg>` | 最终助手消息 stopReason `error`，errorMessage 为 msg（`_` 代表空格）；未给 `!text` 时不输出文本 |
| `!retry` | 先输出一条 stopReason `error` 的助手消息，再输出成功的回复（模拟自动重试） |
| `!crash` | 回复的第一段文本发出后以 3 退出 |
| `!noabort` | 忽略 abort：永不响应，继续休眠 |
| `!rejectprompt` | prompt 响应 `success:false`，error 为 `rejected` |
| `!handled` | prompt 响应 `data: {"disposition":"handled"}`，不发出任何事件 |
| `!nosession` | 本轮不写会话文件（模拟首轮在落盘前失败） |
| `!usage=I,O[,CR[,CW[,R]]]` | 本轮每条已结束的助手消息带这份用量（输入、输出、缓存读、缓存写、推理）；默认全为 0 |
| `!respmodel=<name>` | 助手消息带 `responseModel` |
| `!toolusage=I,O[,CR[,CW]]` | 工具结果消息带这份用量 |
| `!compact=I,O[,CR[,CW]]` | 最终回复前输出 `compaction_start`/`compaction_end`，`result.usage` 为这份用量 |
| `!warm=I,O[,CR[,CW]]` | 最终回复前输出一条缓存预热的用量条目（`entry_appended`，`kind` 为 `cache_warm`） |
| `!warmafter=I,O[,CR[,CW]]` | `agent_settled` 约 50ms 后输出一条缓存预热的用量条目（两轮之间） |

助手消息结束时都带 `durationMs`；`!crash` 退出前向 stderr 写一行 `fakepi: 模拟崩溃`。
