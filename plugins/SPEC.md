# Loop Now 插件系统（第二版）实施规范

状态：2026-10-27 与用户逐项讨论后定稿，第一版插件系统整体拆除、由本规范替代。三个实现会话（server、plugin、web）以本文为准；本文只写合同与边界，实现细节各会话自定并在报告里说明。与本文冲突的旧代码、旧文档一律以本文为准。

## 0. 已确认的决定

1. 插件分两半：**Agent 半边**跑在该 Agent 的 Pi 进程里，就是标准的 Pi 扩展，原样暴露 Pi 的扩展 API；**平台半边**跑在一个共享的常驻 Node 宿主进程里（VS Code 扩展宿主模式），不是每插件一进程。
2. Hook 分四种：拦截（可阻止）、修改（顺序合并）、认领（第一个处理者赢）、观察（并发、结果忽略、排队不丢）。每种有默认超时和超时策略。
3. 宿主给平台半边四样公共设施：持久状态、密钥保管、事件带够上下文（正文按权限另取）、配置表单由平台按 JSON Schema 生成。外发 HTTP 的重试与投递记录放在 SDK 库里，不进宿主；投递记录按约定的状态键写入，插件页读出展示。
4. 包格式：一个目录加清单；两半都可选；安装来源为市场索引仓库、git、本地目录；先在项目启用，再按 Agent 授权；SDK 是 npm 包。
5. 不做沙箱。信任靠：来源分级、安装前展示组件与权限、启用时逐项确认、全局一键停用、日志归到插件。签名以后再加。
6. webhook 通知做成随平台发布的官方插件，既是示例也替代外部身份收件箱的轮询。
7. 项目只能由人（外部身份或控制台）创建，与插件无关，已上线。
8. 旧系统（`plugins/host|sdk|builtin|pi|playwright`、`internal/server/platformplugin`、`web/src/features/plugins`、迁移 022 的表、相关 API）全部拆除，Playwright 按新格式重写。

两处对已确认内容的细化（见 §2）：清单放在 `package.json` 里而不是单独文件，这样 Agent 半边就是一个标准的 Pi 包，任何现成的 Pi 包不改一字就能装进 Loop Now；给 Agent 的 MCP 服务器用 Pi 自己的 `mcp.json` 格式声明，由平台写进该 Agent 的 Pi 目录，不再经平台代理。

## 1. 总体结构

```
┌──────────────┐  stdio JSON 行（控制、事件、确认、日志）  ┌──────────────────────┐
│  Go 服务      │ ───────────────────────────────────────▶ │  插件宿主（Node，常驻） │
│  loop-server  │ ◀─────────────────────────────────────── │  加载所有平台半边       │
│              │  HTTP /api/v1（插件令牌，按权限范围）     └──────────────────────┘
│              │
│              │  启动 Pi 时：-e 扩展文件、--skill、mcp.json、PATH、环境变量
│              │ ───────────────────────────────────────▶ ┌──────────────────────┐
└──────────────┘                                           │  Agent 的 Pi 进程       │
                                                           │  加载已授权插件的 Agent 半边 │
                                                           └──────────────────────┘
```

- Go 服务负责：安装与版本、启用与授权、事件产生与持久化、宿主进程监护、插件令牌与权限校验、给 Pi 组装参数。
- 宿主负责：加载平台半边、分发事件、执行 Hook、提供状态与密钥、收集日志。宿主自身不含任何业务逻辑。
- Agent 半边由 Pi 负责加载和生命周期，平台只负责把文件和配置放到位。

## 2. 包格式与清单

一个插件是一个目录（本地、git 克隆或 npm 包），`package.json` 是唯一清单：

```json
{
  "name": "@example/loop-now-webhook",
  "version": "1.2.0",
  "description": "收件箱有新消息时 POST 到回调地址",
  "keywords": ["loop-now-plugin", "pi-package"],
  "pi": {
    "extensions": ["./agent/index.ts"],
    "skills": ["./skills"]
  },
  "loopNow": {
    "id": "webhook",
    "displayName": "Webhook 通知",
    "minPlatformVersion": "2026.11",
    "platform": "./platform/index.ts",
    "events": ["inbox.received"],
    "permissions": ["inbox.read", "state"],
    "configSchema": { "...": "JSON Schema；secret 字段加 \"x-secret\": true；项目级可覆盖的字段加 \"x-scope\": \"project\"" },
    "accounts": [{ "kind": "lark", "label": "飞书账号", "fields": { "...": "JSON Schema" }, "multiple": true }],
    "mcpServers": { "browser": { "command": "node", "args": ["./mcp/server.js"], "env": { "PROFILE_DIR": "${LOOP_NOW_PLUGIN_DATA}/profiles/${LOOP_NOW_AGENT_ID}" } } },
    "bin": ["./bin"],
    "actions": [{ "id": "resend", "label": "重发", "where": ["identity"], "fields": { "...": "JSON Schema" } }],
    "http": true,
    "schedule": [{ "id": "sweep", "cron": "*/10 * * * *" }],
    "channels": [{ "id": "lark", "label": "飞书" }]
  }
}
```

规则：

- `pi` 段是 Pi 的标准包清单（extensions、skills、prompts），语义以 Pi 文档 `packages.md` 为准。没有 `pi` 段也没有 `loopNow` 段的目录不是插件。
- `loopNow.id` 是平台内唯一标识，小写字母、数字、连字符，安装后不可改；`name` 是 npm 名，两者无关。
- `loopNow.displayName` 与顶层 `description` 可以是字符串，也可以是按语言的对象，如 `{ "zh-CN": "Webhook 通知", "en-US": "Webhook notifications" }`。
  - 对象至少一项；键为语言标签，按 BCP 47 的写法，须匹配 `^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$`（如 zh-CN、en-US、en）；值去掉首尾空白后非空。不合格的对象，以及数字、数组、布尔、null，一律拒绝安装。
  - 字符串没有指明语言，各语言都显示它。`displayName` 为空或缺省时回落到 npm 名，再回落到 `id`。
  - 平台原样保存并原样返回，保留作者写的键顺序。
  - 控制台按当前语言取，没有时依次用 zh-CN、en-US、作者写的第一项；服务端没有语言的地方（插件表的显示名、插件发件人名的兜底）依次用 zh-CN、en-US、第一项。
  - 发布到 npm 的插件，npm 期望 `description` 是字符串；只从 git、本地目录或市场安装的插件不受影响。
- `loopNow.platform` 缺省表示没有平台半边；`pi.extensions` 为空表示没有 Agent 半边。只带 skills 的也是合法插件。
- **清单说了算**：平台半边在代码里订阅的事件、调用的平台接口范围，必须在 `events`、`permissions` 内，超出的订阅在加载时报错并让插件进入"错误"状态；超出范围的 API 调用返回 403 并记录到插件日志。
- `configSchema` 由平台渲染成表单；`x-secret` 字段只写不回显；`x-scope: project` 字段在项目启用时可覆盖，其余为全局值。
- `mcpServers` 的条目格式与 Pi `mcp.json` 完全一致，可用 `${...}` 引用环境变量；平台在启动 Pi 前把所有已授权插件的条目合并写入该 Agent 的 Pi 目录 `mcp.json`，并给 Pi 加 `-e builtin:mcp`（因为 `--no-extensions` 会同时关掉内置 MCP）。相对路径以插件安装目录为基准。
- `bin` 列出的目录在启动 Pi 前加入该 Agent 的 PATH。
- `accounts` 声明账号种类；账号由人在项目里添加，按 Agent 授权；授权后以环境变量注入该 Agent 的 Pi 和 MCP 进程，变量名 `LOOP_NOW_ACCOUNT_<KIND>_<FIELD>`（多账号时为 JSON 数组 `LOOP_NOW_ACCOUNTS_<KIND>`），同时平台半边可用 `api.accounts.list(projectId, kind)` 读取。
- `minPlatformVersion` 按平台的发布版本比较（形如 `2026.11`），不满足时拒绝安装。
- 安装目录为 `<work root>/platform/plugins/<id>/<version>/`，数据目录为 `<work root>/platform/plugins/<id>/data/`（跨版本保留，卸载时询问是否删除）。两者分别以环境变量 `LOOP_NOW_PLUGIN_ROOT`、`LOOP_NOW_PLUGIN_DATA` 提供给宿主和 Pi。
- 依赖安装：npm 与 git 来源在安装时执行 `npm ci --ignore-scripts --omit=dev`（无锁文件则 `npm install --ignore-scripts --omit=dev`）；本地目录来源不安装不修改，依赖由作者负责（与 Pi 一致）。

### 2.1 Agent 半边给 Pi 的东西

启动某个 Agent 的 Pi 时，平台按该 Agent 已授权的插件：

| 清单项 | 处理 |
|---|---|
| `pi.extensions` | 每个文件一条 `-e <绝对路径>`，在平台自己的 `-e`（认证、服务等级）之后 |
| `pi.skills` | 每个目录一条 `--skill <绝对路径>`（已用 `--no-skills`，显式路径仍加载） |
| `pi.prompts`、`pi.themes` | 第一版不处理 |
| `loopNow.mcpServers` | 合并写入 `agents/<id>/pi/mcp.json`，并加 `-e builtin:mcp` |
| `loopNow.bin` | 加入 PATH |
| 账号与配置 | 环境变量：`LOOP_NOW_PLUGIN_<ID>_CONFIG`（JSON，不含 secret 字段）、账号变量见上 |
| `${LOOP_NOW_PLUGIN_ROOT}`、`${LOOP_NOW_PLUGIN_DATA}`、`${LOOP_NOW_AGENT_ID}` | 一个 Pi 会装多个插件，所以这三个占位符在合并 mcp.json 时按各自插件替换，`./`、`../` 相对安装目录解析；其余 `${…}` 留给 Pi |

Agent 半边调用平台接口用现成的 loopctl 和该 Agent 的进程令牌，不另发令牌；Agent 半边的权限就是该 Agent 的权限。

Pi 扩展的 `ctx.ui` 对话类调用（confirm、select、input）在 RPC 模式下会以 `extension_ui_request` 发到平台：第一阶段平台按请求自带的超时默认值回应，第三阶段把它变成对话里的一个提问。

### 2.2 平台半边的 API

```ts
import type { PluginAPI } from '@loop-now/plugin-sdk';
export default function register(api: PluginAPI) {
  api.on('inbox.received', async (event, ctx) => { /* 观察 */ });
  api.on('node.before_submit', async (event, ctx) => ({ block: true, reason: '...' })); // 拦截
  api.on('sleep.before_schedule', async (event) => ({ patch: { durationSeconds: 600 } })); // 修改
  api.on('console.command', async (event) => ({ handled: true, reply: '...' })); // 认领
  api.actions.register('resend', async (input, ctx) => ({ notice: '已重发' }));
  api.http.handle(async (req) => new Response('ok')); // 平台把 /plugins/<id>/http/* 转到这里
  api.schedule.on('sweep', async () => {});
  api.channels.register('lark', { inbound: ..., outbound: async (message) => {} });
  api.state.get/set/delete/list(key[, { projectId }]);
  api.config.get([projectId]); api.secrets.get(name); api.accounts.list(projectId, kind);
  api.platform.fetch('/agents/...', { method, body }); // 带插件令牌的 /api/v1
  api.log.info/warn/error(...);
}
```

- `register` 可以是异步的；宿主等它返回后才认为插件就绪。工厂里不要起定时器或连接，放到 `api.lifecycle.on('start' | 'stop')`。
- 所有注册都返回取消函数，插件停用或卸载时宿主统一释放；作者不手动清理。
- `ctx` 带 `eventId`、`projectId`、`agentId`、`signal`（超时或停用时触发）。

## 3. 宿主进程

- 平台启动时拉起一个宿主：`node <平台内置 host 入口>`，工作目录为 `<work root>/platform/plugins/`，`stdin/stdout` 走协议，`stderr` 逐行进平台日志并标注来源插件（宿主在每行前缀 `[<id>]`）。
- 协议的权威定义在 `api/plugin-host-protocol.md`（server 维护，plugin 会话按它实现宿主）。概要：每行一个 JSON，`{"id","type","plugin","payload"}`。Go→宿主：`load`、`unload`、`config_changed`、`event`（观察，需 ack）、`call`（拦截/修改/认领，同步等结果）、`action`、`http`、`schedule`、`shutdown`。宿主→Go：`ready`、`ack`、`result`、`log`、`state_get/state_set`（宿主不直连数据库，状态读写经 Go）、`platform_call`（可选；默认插件直接用 HTTP）。
- 事件持久化：Go 在事务提交后把事件写入 `plugin_events`，按订阅生成 `plugin_event_deliveries(plugin_id, event_id, status, attempts, last_error)`。观察类事件至少送达一次，宿主处理完回 `ack` 才标完成；宿主重启后从未完成处继续，顺序按事件时间，同一插件串行、不同插件并行。
- 同步类（拦截、修改、认领）不持久化；按注册顺序逐个调用；默认超时 15 秒；超时策略：拦截默认放行（清单可声明 `"failClosed": true` 改为阻止），修改默认跳过该插件，认领默认当作未处理。超时只是不再等待，宿主随后向插件发 `signal`。
- 崩溃策略：宿主退出即重启，5 分钟内第 3 次退出后停止重启，所有插件标为"宿主已停止"，插件页显示崩溃前最后在处理的事件和插件。人可在页面上"重新启动宿主"。
- 一键停用：全局设置里"停用全部插件"，Go 停止宿主并不再给 Pi 挂载任何 Agent 半边；重新启用时恢复。
- 版本更新：安装新版本后旧版本进程内卸载、新版本加载（`unload` 再 `load`），任务里的 Agent 不受影响（它们已经启动的 Pi 继续用旧文件直到自然重启）。

## 4. 事件目录

事件记录的固定字段：`id`（ULID）、`type`、`at`、`projectId|null`、`agentId|null`、`taskId|null`、`runId|null`，加该类型自己的字段。正文一律不带，用权限范围内的 API 取。

| 种类 | 事件 | 结果 |
|---|---|---|
| 拦截 | `run.before_start`（origin、messageId）、`task.before_start`、`node.before_start`、`node.before_submit`（可拒绝这次提交：节点保持原状，Agent 看到插件给的原因后可以改了再交）、`inbox.before_send`（from、to） | `{ block: true, reason }` 或空 |
| 修改 | `message.before_enqueue`（控制台或渠道发给 Agent 的消息，可改 text、attachments）、`inbox.before_deliver`（Agent 发出的消息送达前）、`sleep.before_schedule`（durationSeconds、wakeCondition） | `{ patch }`，按字段合并 |
| 认领 | `console.command`（对话框里的 `/xxx`） | `{ handled: true, reply }` |
| 观察 | `run.queued`、`run.started`、`run.ended`（status、endReason、tokens）、`task.started|waiting|blocked|completed|cancelled`、`node.started|submitted|blocked`、`inbox.received|read|cancelled`（from、to、threadId、isQuestion、excerpt≤80）、`question.asked|answered`、`sleep.requested|woken`、`agent.created|updated|deleted`、`pi.started|exited`（reason）、`model.retry`、`context.compacted`、`folder.changed`（层级、路径、改者）、`plugin.enabled|disabled|config_changed`、`channel.outbound`（Agent 回复了某个插件发来的消息，送给该插件的渠道处理器，需 ack） | 无 |

事件名与 `lifecycle_events` 现有的类型名尽量一致；新增的由 server 在 `api/openapi.yaml` 的 `PluginEvent` 模式里逐个列出字段，作为对插件作者的合同。

## 5. 权限范围

清单 `permissions` 可选值（第一版）：

`agents.read`、`agents.write`（改配置、唤醒）、`tasks.read`、`tasks.write`（建任务、节点动作）、`runs.read`、`inbox.read`（含正文）、`inbox.send`、`folders.read`、`folders.write`（项目与全局上下文文件夹）、`identities.read`、`state`、`accounts.read`。

事件订阅不占权限位，由 `events` 单独声明；安装时两者都展示。插件令牌（`lnp_` 前缀）由 Go 按插件签发，只含声明的范围，宿主按插件注入；HTTP 层对每个操作标注所需范围（`x-plugin-scope`），超出返回 403。

## 6. 安装、启用、授权、信任

- 来源：`loopctl plugin install <market-name>[@version] | <git url>[@ref] | <local dir>`；`plugin update <id>`、`plugin remove <id>`、`plugin list`、`plugin validate <dir>`、`plugin pack <dir>`。页面上同样三种来源。
- 市场：一个 git 仓库，根目录 `loop-now-market.json`：`{ "name", "plugins": [{ "id", "source": "git:...@ref" | "npm:...", "version", "displayName", "description", "verified": true }] }`；`version` 是该条目当前版本，检查更新按它和已装版本比较；`displayName`、`description` 可以按语言写，规则同 §2。官方市场地址来自服务设置 `LOOP_NOW_PLUGIN_MARKET_URL`（官方市场建立前为空），全局设置里可加别的市场。
- 信任分级：官方市场和 `verified` 的直接装；其他来源在页面上要确认，CLI 要 `--force`。安装前展示：两半各有什么（扩展文件数、Skill、MCP 服务器、CLI、操作按钮、渠道）、订阅的事件、权限范围、配置项、账号种类。
- 项目启用：在项目插件页启用并填写项目级配置；未启用的项目不产生事件给该插件，该项目的 Agent 不能被授权。
- Agent 授权：在 Agent 配置页按插件勾选，并选择账号；授权后下次 Pi 启动生效（与上下文入口一致，不打断当前回合）。
- 版本固定：安装时记录精确版本（npm 版本、git 提交），更新是显式操作；任务进行中不换文件。
- 官方插件随平台发布：`webhook`（第一阶段）、`playwright`（第二阶段）、`hello`（两半各一个最小示例，兼作文档）。官方插件源码放在 `plugins/official/<id>/`，平台启动时注册为"内置来源"，安装与第三方一样走流程。

## 7. 页面

- 插件市场（全局）：已安装列表（状态、版本、启用项目数、授权 Agent 数）、安装入口（市场浏览、git、本地目录）、全局停用开关、宿主状态。
- 插件详情：概览（清单展示）、配置表单、账号、日志（宿主日志按插件过滤，保留 7 天）、事件（最近投递与失败）、投递记录（读约定状态键 `deliveries/*`）、操作按钮（where 为 global 的）。
  投递记录的约定：SDK 的重试库把每次投递写到状态键 `deliveries/<ULID>`，值为 `{ "id", "at", "eventId", "eventType", "target", "status": "pending" | "delivered" | "failed" | "abandoned", "attempts", "nextAttemptAt" | null, "lastStatusCode" | null, "lastError" | null, "updatedAt" }`；插件页按键前缀 `deliveries/` 读出、按 `at` 倒序显示；库保留最近 500 条，更早的自动删除。
- 项目插件页：启用开关、项目级配置、账号管理。
- Agent 配置页插件段：授权勾选与账号选择（沿用现有紧凑列表交互）。
- 控制台操作按钮：按清单 `where`（`agent`、`task`、`project`、`identity`、`global`）出现在对应页面，带表单弹窗，结果以提示显示。
- 设计：先出设计稿到现有画布风格（A4），用户看过再实现。

## 8. 数据

新迁移 031：

- `plugins(id text pk, display_name, source jsonb, version, manifest jsonb, install_dir, status, last_error, installed_at, updated_at)`
- `plugin_versions(plugin_id, version, manifest, install_dir, digest, installed_at)`
- `plugin_config(plugin_id, project_id null, values jsonb, secrets jsonb, revision)`（项目级覆盖一行一项目）
- `project_plugins(project_id, plugin_id, enabled)`
- `agent_plugin_grants(agent_id, plugin_id, account_ids jsonb)`
- `plugin_accounts(id, plugin_id, project_id, kind, name, values jsonb, revision, deleted)`
- `plugin_state(plugin_id, project_id null, key, value jsonb)`
- `plugin_events(id, type, at, project_id, agent_id, task_id, run_id, data jsonb)`
- `plugin_event_deliveries(plugin_id, event_id, status, attempts, last_error, acked_at)`
- `plugin_tokens(plugin_id, token_hash, scopes jsonb)`
- `plugin_host_log(at, plugin_id null, level, line)`（或文件日志，server 定）

拆除：迁移 022 的全部表与 `task_records.platform_plugins_captured`；迁移 005 的表如确认无其他用途一并删除（server 先核对 `agent_credentials.template_source` 与 CLI 凭证模板的关系，有用则保留 005）。本机数据库里旧表内容不迁移，直接删除（用户已同意全部推倒；Playwright 旧包未在任何项目启用）。任务快照里的 `PluginScope` 等字段删除，快照版本升级，旧任务读取时按无插件处理。

## 9. 分步与分工

| 阶段 | server（Go） | plugin（Node） | web |
|---|---|---|---|
| 1 | 拆旧系统（Go、API、迁移、快照字段）；迁移 031；宿主进程监护与 stdio 协议；事件产生与持久化（观察类全部）；插件令牌与 `x-plugin-scope`；安装（本地、git、npm）、版本、启用、授权的 API；给 Pi 组装参数（§2.1）；`loopctl plugin *`；OpenAPI 全部定稿 | 宿主：协议、加载、卸载、观察类分发与 ack、状态、密钥、日志；SDK 包 `@loop-now/plugin-sdk`（类型、`api.*`、重试投递库）；官方插件 `webhook`、`hello`；`plugin validate` 的校验库供 Go 调用 | 拆旧页面；新插件市场、详情、项目插件、Agent 授权页；配置表单生成；日志与投递记录视图；全局停用 |
| 2 | 账号、MCP 合并写入、PATH、环境变量注入；`extension_ui_request` 的默认回应 | 官方插件 `playwright` 重写（Pi 扩展 + mcpServers 声明）；SDK 的账号与配置读取 | 账号页面；Agent 授权的账号选择 |
| 3 | 拦截、修改、认领三类事件的同步调用点；控制台操作按钮 API；渠道与 HTTP 入口转发；定时；市场索引与更新 | 宿主的同步调用、超时与 `signal`；渠道与 HTTP 支持；`extension_ui_request` 转提问 | 操作按钮；渠道配置；市场浏览与更新 |

每阶段结束各会话给出：改动文件、测试范围与结果、未覆盖项。主协调会话验收后合并发布。

## 10. 不做的事

- 不做沙箱、不做签名（以后再说）。
- 不迁移旧插件数据。
- 不在宿主里放业务逻辑；平台能力只通过事件和 HTTP API 暴露。
- 不让 Agent 令牌获得管理权限；Agent 半边的权限等于该 Agent 的权限。

## 11. 第三阶段合同细化（2026-10-27 补）

- **插件作为发件方**：`inbox.send` 范围让插件以发件方 `{ kind: "plugin", pluginId, channelId?, displayName }` 给 Agent 发消息（`POST /inbox/messages` 的插件版），在对话里像外部身份一样显示；Agent 的回复按线索路由回该插件，以 `channel.outbound` 事件送达（含 threadId、messageId、文本摘要，正文用 `inbox.read` 取），插件再发到飞书之类。Agent 在插件线索里回复时可以带选项提问（2026-10-28 补，迁移 033）：`channel.outbound` 带 `isQuestion` 与 `question: { id, options: [{ id, label }], allowText, deadline | null } | null`，形状与 `identity.inbound` 相同；插件用 `inbox.send` 范围回复该问题（`optionId` 或 `text`，SDK `api.channels.answer`）即作答，效果与外部身份作答一致（已回答、任务由等待转回、提醒 Agent），审计 actor 为 plugin。插件自己不能发出问题（带 options 返回 422），回复不属于自己的线索返回 403。宿主 load 与 `config_changed` 载荷里的 `config.projects` 只含启用了该插件的项目（SDK `api.config.projects()`）。平台不接收渠道的原始消息，所以没有 `channel.inbound` 认领事件：渠道插件自己通过 HTTP 入口或长连接收消息，再调 `inbox.send`。
- **HTTP 入口**：清单 `http: true` 的插件在外部 TLS 口得到无令牌路径 `/hooks/<id>/*`（供飞书等回调），在控制台口得到 `/api/v1/plugins/<id>/http/*`（需控制台或插件令牌）。平台把方法、路径、头、正文（≤ 1 MiB）转给宿主 `http` 请求，30 秒超时，原样返回插件的状态码、头和正文；签名校验由插件自己做；每插件每秒 20 次限流。
- **控制台操作按钮**：`POST /plugins/{id}/actions/{actionId}`，体为 `{ where: { projectId?, agentId?, taskId?, identityId? }, input }`，转宿主 `action` 请求，30 秒超时，返回 `{ notice, url? }`；页面按清单 `where` 决定在哪些页面显示按钮，按钮带按 `fields` 生成的表单。
- **定时**：清单 `schedule[]` 由 Go 按 cron 触发宿主 `schedule` 请求，同一条目不并发，错过的不补；不再有 `schedule.tick` 事件。
- **市场**：市场是一个 HTTPS 可取到的 `loop-now-market.json`（git 仓库 raw 地址即可），`GET /plugin-markets` 汇总各市场的条目并标出已安装与可更新，`POST /plugin-previews` 的来源增加 `{ kind: "market", market, id, version? }`；检查更新按市场里的最新版本比较。
- **保留期**：`plugin_events` 与投递记录保留 30 天，由 server 定时清理（宿主日志 7 天不变）。
- **`extension_ui_request`**：第二阶段平台按请求自带的超时默认值回应；第三阶段把 `confirm`、`select`、`input` 转成该 Agent 对话里的一个提问，回答后回给 Pi。

## 12. 插件代管外部身份（2026-10-28 补，用户决定）

目的：任务或常态里的 Agent 要人决定时，给某个外部身份发消息或提问；这个身份由一个插件的渠道代管，插件把消息推到飞书、Slack、邮件等，再把人的回复以该身份的名义送回平台。平台只做机制，具体渠道由插件作者实现；官方 `webhook` 插件扩展为参考实现。Pi 扩展的对话框在任务回合里继续按取消处理。

- **清单**：`loopNow.channels[]` 的条目可加 `"manageIdentities": true`，表示该渠道能代管外部身份；需要权限 `identities.manage`。安装预览里展示"可代管外部身份"。
- **绑定**：外部身份多一个字段 `managedBy: { pluginId, channelId } | null`。`PUT /external-identities/{id}/manager`（只限控制台，与其他身份操作一致）设置或清空；只能选已安装且渠道声明了代管的插件；一个身份最多一个代管者。插件停用或宿主停止时绑定保留、页面提示"代管插件未运行"；卸载插件时清空绑定。外部身份是平台级的，不看项目启用。
- **入站事件** `identity.inbound`（观察类，只送给代管插件，需 ack，排队不丢）：`{ identityId, messageId, threadId, from: { kind, agentId|null, name }, projectId|null, taskId|null, nodeRunId|null, isQuestion, question: { id, options: [{ id, label }], allowText, deadline|null } | null, excerpt }`。正文和线索用 `identities.manage` 范围下的身份收件箱读取接口取（`GET /external-identities/{id}/inbox`、`.../inbox/{messageId}` 对代管插件开放，只限它代管的身份）。
- **替身份回复**：插件令牌调 `POST /inbox/messages/{messageId}/reply` 与回答提问的现有接口时可带 `asIdentityId`；只允许它代管的身份，否则 403；审计记 actor `plugin`、`onBehalfOf` 身份。回复后对发起方（Agent、任务节点）的效果与该身份亲自回复完全一致：提问被回答、任务由等待转回。
- **SDK**：`api.channels.register(id, { identityInbound: async (event, ctx) => {} })`；`api.identities.read(identityId, messageId)`、`api.identities.reply(identityId, messageId, { text })`、`api.identities.answer(identityId, questionId, { optionId } | { text })`；本地先校验 `identities.manage` 与渠道声明。
- **webhook 参考实现**：新增配置 `publicBaseUrl`（对方回调用的平台外部地址）与 `manageIdentities` 开关；代管身份收到消息或提问时 POST 事件本体，提问附 `question.options` 与 `answerUrl = <publicBaseUrl>/hooks/webhook/answer`；对方 POST `{ messageId, questionId?, optionId? | text }` 到该入口，插件用同一密钥验签后调 `api.identities.answer` 或 `reply`。README 写明如何照此接飞书等渠道。
- **页面**：外部身份页加"代管"选择（列出可代管的插件与渠道、当前状态）；插件详情"包含"里列出可代管的渠道；代管插件未运行时身份页提示。
- 存储：迁移 032（`identity_managers`、审计的 `on_behalf_of`）。代管插件对被代管身份的消息只收到 `identity.inbound`，不再同时收到 `inbox.received`。
- **分工**：server 做清单字段、绑定接口、`identity.inbound`、`asIdentityId`、`identities.manage` 范围与审计；plugin 做 SDK 与 webhook 扩展；web 做身份页与插件页展示。做完一起部署。
