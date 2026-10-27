# 插件宿主协议（第 1 版）

Go 服务与插件宿主（Node 常驻进程）之间的 stdio 协议，补充 `plugins/SPEC.md` §3。Go 侧实现在 `internal/server/pluginhost`，测试用假宿主在 `internal/server/pluginhost/fakehost_test.go`。

## 进程

- Go 以 `node <宿主入口>` 启动宿主，工作目录 `<work root>/platform/plugins/`，单独的进程组；退出或被停止时整个进程组一起结束。
- **标准输出只写协议行**。插件的 `console.log` 等输出必须改到标准错误或 `log` 消息，否则会破坏协议。
- 标准错误逐行进平台日志与插件日志（级别 warn）。插件产生的行前缀 `[<插件 id>] `，Go 据此归到该插件；没有前缀的归宿主自身。

## 行格式

每行一个 JSON 对象，UTF-8，以 `\n` 结尾，单行最多 16 MiB：

```json
{"id": "g12", "type": "load", "plugin": "webhook", "payload": {}}
```

- `id`：请求由发起方编号，在自己的方向内唯一（Go 用 `g<n>`，宿主建议 `h<n>`）；对它的回应带同一个 `id`。不需要回应的消息（`ready`、`log`、`shutdown`）不带 `id`。
- `plugin`：消息涉及的插件 id；宿主自身的消息省略。
- 回应载荷统一为 `{"ok": true, "value": <任意，可省略>}` 或 `{"ok": false, "error": "<原因>"}`。

## 启动与就绪

1. 宿主启动后第一行写 `{"type":"ready","payload":{"protocol":1,"hostVersion":"<版本>"}}`。Go 在 `ready` 之前不发任何消息；30 秒内没有 `ready`、或 `protocol` 不是 1，按一次启动失败处理。
2. Go 随后按顺序对每个有平台半边的插件发 `load`，等到它的 `result` 再发下一个。

## Go → 宿主

| type | 载荷 | 宿主回应 | Go 的时限 |
|---|---|---|---|
| `load` | 见下 | `result`：`register` 返回（含异步完成）后 ok；抛错、订阅或权限超出清单时 ok 为 false，`error` 写原因 | 30 秒 |
| `unload` | `{}` | `result`：停止 lifecycle、释放全部注册后 ok | 30 秒 |
| `config_changed` | `{"config": <同 load.config>}` | `result` | 30 秒 |
| `event` | `{"event": <事件对象>, "attempt": n, "timeoutMs": n}` | `ack`：处理完成 ok；处理函数抛错 ok 为 false | 60 秒 |
| `call` | `{"kind": "intercept"\|"modify"\|"claim", "event": <事件对象>, "timeoutMs": n}` | `result`，`value` 见下 | 15 秒 |
| `action` | 见下 | `result`，`value` 为 `{"notice": "...", "url": "..."}`（url 可省略） | 30 秒 |
| `http` | 见下 | `result`，`value` 为 `{"status": 200, "headers": {"name": ["value"]}, "body": "<base64>"}` | 30 秒 |
| `schedule` | `{"scheduleId": "sweep", "cron": "*/10 * * * *", "at": "<应触发时间>", "timeoutMs": n}` | `result`：处理完回 ok | 10 分钟 |
| `shutdown` | 无 | 不回应；卸载全部插件后自行退出，5 秒内未退出 Go 结束进程组 | 5 秒 |

`load` 的载荷：

```json
{
  "id": "webhook", "version": "1.2.0",
  "root": "<安装目录>", "entry": "<平台半边入口的绝对路径>", "dataDir": "<数据目录，跨版本保留>",
  "manifest": { "events": [], "permissions": [], "failClosed": false, "configSchema": {}, "accounts": [], "actions": [], "channels": [], "schedules": [], "http": false },
  "config": { "values": {}, "secrets": {}, "projects": { "<projectId>": { "values": {}, "secrets": {} } } },
  "token": "lnp_…", "apiUrl": "http://127.0.0.1:8780/api/v1"
}
```

- `config` 是原样的保存值：全局一份，加上每个有项目级配置的项目一份（只含 `x-scope: project` 字段）。`api.config.get(projectId)` 由宿主合并：全局 values 与 secrets 之上叠加该项目的。`config_changed` 的载荷是 `{"config": <同上>}`，每次给出全部。
- `token` 只在本次加载有效：每次加载签发新令牌并吊销这个插件的旧令牌（宿主重启、更新版本都会重新加载）。宿主用它带 `Authorization: Bearer` 调用 `apiUrl`，只能调用接口上标了 `x-plugin-scope` 且在 `permissions` 内的操作。
- 宿主应在加载时检查插件代码订阅的事件都在 `manifest.events` 内，超出时回 `ok: false`。

事件对象 = 固定字段 `id`（ULID）、`type`、`at`（RFC 3339）、`projectId`、`agentId`、`taskId`、`runId`（没有时为 null）加该类型自己的字段；同名时固定字段优先。

`action` 的载荷：`{"actionId": "resend", "where": {"projectId"?, "agentId"?, "taskId"?, "identityId"?}, "input": {...}, "timeoutMs": n}`，input 已按清单 fields 校验。
回应 ok 为 false 时控制台得到 502 plugin_failed，`error` 作为说明。

`http` 的载荷：`{"via": "external" | "console", "method": "POST", "path": "/callback", "query": "a=1", "headers": {"content-type": ["application/json"]}, "body": "<base64>", "timeoutMs": n}`。
`path` 是去掉 `/hooks/<id>` 或 `/api/v1/plugins/<id>/http` 之后的部分（以 `/` 开头），头名小写，不含 `Authorization` 与 `Cookie`；正文不超过 1 MiB。
回应的状态码、头与正文原样返回给调用方（不允许 `Connection`、`Transfer-Encoding` 等逐跳头）；ok 为 false 时调用方得到 502。

`schedule`：Go 按清单 `schedule[]` 的五段 cron（服务端本地时间）触发；同一条目上一次还没有回应时这一次跳过，错过的（宿主停止、服务端停机）不补。

`call` 的 `value`：

- `intercept`：`{"block": true, "reason": "..."}` 阻止，其余放行；
- `modify`：`{"patch": {...}}`，按字段合并到事件，下一个插件看到合并后的事件；
- `claim`：`{"handled": true, ...}` 认领，整个对象交给调用点，其余当作未处理。

时限到了 Go 不再等待，按种类处理（拦截放行、清单 `failClosed` 的插件阻止；修改跳过；认领当作未处理）。宿主应在 `timeoutMs` 到时触发该次调用的 `ctx.signal`；迟到的回应被丢弃。

## 宿主 → Go

| type | 载荷 | Go 回应 |
|---|---|---|
| `log` | `{"level": "debug"\|"info"\|"warn"\|"error", "message": "..."}` | 不回应 |
| `state_get` | `{"projectId": <uuid 或 null>, "key": "..."}` | `result`，`value` 为保存的值，没有时为 null |
| `state_set` | `{"projectId", "key", "value": <任意 JSON>}` | `result` |
| `state_delete` | `{"projectId", "key"}` | `result`（不存在也成功） |
| `state_list` | `{"projectId", "prefix": "..."}` | `result`，`value` 为 `[{"key","value"}]`，按 key 排序，最多 1000 项 |
| `accounts_list` | `{"projectId": "<uuid>", "kind": "lark"}` | `result`，`value` 为 `[{"id","kind","name","values"}]`，按添加先后，不含已删除的；`values` 含全部字段（包括 secret 字段） |

`state_*` 只对已加载、清单声明了 `state` 权限的插件有效；`projectId` 为 null 表示插件全局；key 为 1 到 512 字节，value 最多 1 MiB。

`accounts_list` 只对要加载的插件（包括 `load` 期间，例如 lifecycle start 里）、且清单声明了 `accounts.read` 权限时有效，`kind` 须是清单 `accounts[].kind` 之一，插件须在 `projectId` 启用。
不满足时 ok 为 false，`error` 依次为：`插件未加载`、`清单没有声明 accounts.read 权限`、`清单没有声明账号种类 <kind>`、`projectId 无效`、`插件没有在这个项目里启用`。

## 观察类事件的投递

- Go 为每个已加载的插件串行投递：取最早（按事件时间）到期的一条，收到 `ack` 才标为完成。不同插件并行。
- 失败（ack 的 ok 为 false、超时、宿主退出）按 5 秒、30 秒、2 分钟、10 分钟重投，第 5 次仍失败后标为 failed 并记入插件日志；重投等待期间，同一插件更晚的事件照常投递。
- 至少送达一次：宿主可能收到重复事件（宿主在处理中退出后重启），按事件 `id` 去重是插件的责任。

## 崩溃与停用

- 宿主意外退出后 1 秒重启；5 分钟内第 3 次退出后不再重启，插件标为宿主已停止，记录退出原因、最后几行标准错误、退出时未回应的请求与最近发出的请求（插件、事件）。人工“重新启动宿主”清零计数。
- 全局停用：Go 发 `shutdown` 让宿主退出，不计入崩溃；重新启用时重新拉起并加载。
