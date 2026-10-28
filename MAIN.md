# Loop Now

更新：2026-10-28。本文是仓库的唯一项目主文档，只保留当前概况、关键约定和需要继续确认的事项。

## 项目是什么

本地运行的通用多 Agent 协作平台。人在 Web 控制台配置项目、Agent、账号和 Skills，与 Manager 对话，由它安排任务。模型接入、代码审查、QA 和线上排查由项目 Prompt、Skills、workflow 承载。定位是一个人带一支 Agent 团队的工作台；插件让团队的能力在平台作者不参与的情况下增长，是开源后活力的来源。

项目源码采用 Apache-2.0，见根目录 `LICENSE`；第三方代码与字体保留原许可证。仓库在 `AI-for-anyone/loop-now`（2026-10-28 由 `hust2018` 转移到该组织并公开，远端只保留 main 分支，首个标签 v0.1.0；三个仓库均已开启 secret scanning 与 push protection），官方插件市场索引仓库是 `AI-for-anyone/loop-now-market`，首个市场插件是 `AI-for-anyone/loop-now-plugin-slack`。

## 模块与代码

| 模块 | 当前能力 | 入口 |
|---|---|---|
| 控制台 | 项目／Agent 配置、聊天、Inbox、任务画布、节点流式对话与产物、运行记录 | `web/src/` |
| 服务端 | Go HTTP 服务、身份权限、配置、任务调度、SSE、持久化 | `internal/server/`、`cmd/loop-server/` |
| 执行器 | 本机 Pi RPC、独立工作区／会话、固定版本 CLI 安装、浅克隆 | `internal/server/executor/local/` |
| 任务与 Inbox | 固定 workflow、分支／回路、重试／取消、提问、排队／打断、sleep | `internal/server/tasks/`、`workflow/`、`inbox/` |
| 账号与工具 | 项目共享模型／CLI 账号；Codex、GitHub、Google、飞书用户认证；连接验证、本机配置扫描导入；gh、lark-cli、gcloud、psql、mysql | `internal/server/cliauth/`、`modelauth/`、`clitemplate/`、`cliimport/`、`cliverify/` |
| 插件系统 | 第二版：Pi 扩展 + 共享 Node 宿主，四种 Hook，清单优先，官方 webhook／hello／playwright | `plugins/`（SPEC、host、sdk、official）、`internal/server/plugins/`、`internal/server/pluginhost/`、`internal/pluginmanifest/`、`web/src/features/plugins/` |
| 平台 CLI | Agent 与外部身份共用的 `loopctl`，按身份开放能力，输出 JSON | `cmd/loopctl/`、`internal/loopctl/` |
| 项目网站 | GitHub Pages 文档站（VitePress，英中双语，Actions 推送 main 自动发布到 `ai-for-anyone.github.io/loop-now/`） | `docs/`、`.github/workflows/pages.yml` |

接口定义在 `api/openapi.yaml` 与 `api/agent-openapi.yaml`；数据库迁移在 `internal/server/store/migrations/`。`auth-helper/` 和 `scripts/lark-user-variant/` 提供认证／运行辅助组件。

## 关键约定

- 层级是 Organization → Project → Agent，另有平台 Agent；目前组织管理未开放。人配置权限，Manager 只能选择已配置的 Agent，不能临时授权。
- 项目 Prompt 自动叠加到项目所有 Agent，冲突时项目规则优先；Prompt 约定不等于程序权限校验。
- 常态 Agent 保留自己的工作区与会话；任务内使用独立副本，同一任务、同一 Agent 连续复用。任务结束回收执行资源并保留记录、会话归档和产物。
- 常态配置从下一条派发消息生效。任务启动后固定 workflow、配置、权限与账号版本；普通重试不会刷新凭证。支持线性、条件与有限回路，暂不支持并行汇合。
- Inbox 默认排队，也可选择打断；打断后按新消息继续。任务节点聊天同样走 Inbox。同节点的多次执行连续展示并标记分界，读历史时保留草稿。
- sleep 是空闲等待，可被排队消息提前唤醒。Agent 每轮自行决定是否再次等待；终点提交并结束本轮后，平台自动完成任务。
- 项目统一保存模型与 CLI 账号，Agent 按权限和选择规则取用；数据库工具可配置多个账号。个人版不设主密钥，凭证以明文保存在 PostgreSQL，需保护数据库访问与备份；模型凭证通过执行作用域文件提供。
- 已支持对阻塞任务显式切换飞书用户账号：固定提测、结果跟进两个成员及目标版本，保留原成果／会话，只安排新的 QA 续行。账号登录成功、切换成功、QA 已发送和 QA 通过是不同结果。
- 写动作使用稳定 actionId；结果不明先查原动作，不另建动作猜测或重复发送。
- 控制台仅本机回环、无登录；Agent 使用进程令牌；外部身份经单独 TLS 端口与明确白名单访问。当前为本机单实例，接受同一系统用户的隔离限制。

## 当前设计方向

- 界面重新设计进行中（逐块定稿后实现）：顺序为导航与范围、概览、统计、插件市场、Skills 库、外部身份与全局设置。导航（方案 A4，设计源 `.local-build/nav-design/project/A4.dc.html`）与统计页（设计源 `.local-build/overview-design/project/Stats.dc.html`）已实现并部署。统计页按用户定下的指标优先级组织：第一屏是无人要求的消耗（定时唤醒占比）、每次回复平均上下文与压缩次数、发出去多久开始回（p50／p90，含排队）、失败与中断及原因、等你回答的问题；"更多·排查时再看"之下是 Agent 体检表、工具调用、模型与进程、一天里有多忙。花费只按 API 单价折算成美元的参考值（价格表在 `web/src/features/stats/pricing.ts`），后端不存价格。概览页新设计（同一画布 `Main.dc.html`）尚未定稿；@console 收件箱已移除；插件系统先讨论再实现。

- 上下文与 Agent 文件夹已在本地部署：项目内 Agent 拿到自己的和本项目的上下文，平台 Agent 拿到自己的和全局上下文，任务内 Agent 自己的部分随任务回收。项目／全局上下文是普通文件夹，默认在 `~/.loop_now`（服务运行用户的主目录），可用环境变量改，人和 Agent 都可修改。常态 Agent 只有一个文件夹，即它的工作目录，也就是它自己的上下文：页面上可上传、浏览、编辑、删除，可一次性从 Git 拉取（可只拉指定分支），不再有文件夹来源与同步；任务开始时复制整个文件夹（APFS 克隆），结束回收，任务里的 Agent 开工前自行 fetch／pull 需要的分支。写入由 Agent 自行判断（决定、纠正、他人需要的事实、交接、提交节点结果时），规则放入内置 Skill，不写过程日志和可查到的信息。每层的入口是文件夹根目录的 `AGENTS.md`；每次 Pi 启动时把该 Agent 可见两层的入口放进系统提示（自己的由 Pi 从工作目录读取，项目或全局的由平台加入），其余文件按需读取。入口修改不即时重载，等 Pi 自然重启（空闲回收或配置变更）后生效。文件组织（一个主题一个文件，入口当目录并记最关键的结论，改者同步入口，过时即改删）由 Agent 自己管理，平台不截断入口。页面文件操作放在 Agent 配置、项目设置和全局设置三处；人和 Agent 同时改时后写覆盖；任务复制与会话分叉在同一时刻进行（基础 Agent 当前这一轮结束后）；从 Git 拉取只拉指定分支、保留该分支完整历史；删除 Agent 保留其文件夹与会话（现状），删除项目保留其上下文文件夹；旧“文件夹来源”停用但保留数据。源码、TEST PG＋fakePi 回归、APFS 克隆实测（20 万文件约 1.7 秒）与真实 React → 隔离服务端端到端 14 项通过；部署时回放了定价的 5 个上传文件。

- 对话排队交互已按新设计重做并在本地部署：排队消息贴在输入框上方逐条列出，任何来源的排队项（自己的控制台消息、其他 Agent 的 Inbox 消息、任务节点消息）都可“转为打断”或取消，自己的消息可编辑（撤回后回填输入框）；Enter 排队，⌘/Ctrl+Enter 直接打断；输入为空时可停止当前回复，之后队列继续，节点没有排队项时转阻塞（已被人停止）。被取消的 Inbox 消息整条作废，双方可见，收件 Agent 只看到取消说明；已读或已取消的消息立即离队。普通对话的提醒改为逐条关联、只点名自己承载的消息（迁移 028）。TEST PG/Redis＋fakePi 回归与真实 React → 隔离服务端端到端 11 项通过，尚未在真实业务中实际操作；设计源文件在 `.local-build/queue-design/`，证据在 `.local-build/chat-queue-ui/redesign-e2e/`。

- 已移除主密钥并在本地部署：删除 `LOOP_NOW_MASTER_KEY` 与加密代码，026 迁移删去加密列、`ciphertext` 改名 `secret`，残留旧加密记录时整体拒绝；任务快照升为 v8，`task_instances.snapshot_sha` 冻结原快照身份，飞书切换的绑定钉不变；本机导入指纹改为普通 SHA-256，旧回执保留不再匹配。转换后不能回到旧版，切换前的 `pg_dump` 备份保留在本批发布目录。飞书续行未在原生运行时下实跑。

- Codex 速度选项已完成源码、隔离验证及本地部署：Agent 新建和模型配置独立选择标准／Fast／Ultrafast，默认标准；常态下一轮生效，新任务捕获固定值，旧任务无此字段维持旧行为。仅针对 `openai-codex`，请求分别使用 `default`／`priority`／`ultrafast`；账号与具体模型的实际可用性由 OpenAI 决定，不静默换档。已验证真实 Pi 1.4 离线请求、TEST PG、会话续用及 Web 表单；025 迁移已应用，现有 13 个项目 Agent 保持默认标准；实际接口与内置适配器已读回，尚未执行真实供应商验证。

- Agent 列表的 sleep 信息已在本地压缩为主行下的唤醒时间／倒计时与单行省略原因，详情保持原展示；共享 Markdown 已启用 Mermaid，完整图表渲染、流式未完整及错误时保留可复制源码。两项定向、类型、局部 lint 与真实 React 桌面夹具检查通过，Mermaid 另通过 API 类型一致性及构建检查。预览在 `.local-build/agent-list-sleep-ui/`、`.local-build/mermaid-ui/`；部署后已核主入口与 Mermaid 动态资产的实际 HTTP 哈希。

- 当前优先继续完善个人本地版 `loop-now/main`。高并发版已隔离到独立私有仓库 `hust2018/loop-now-scalable`，暂不直接推进其改造；用户计划随后通过个人版中的 Agent、任务和插件开发高并发版。个人版维持本地单实例定位。

- 内部 Agent 只读查询同项目 Agent 状态已在本地部署。命令为 `loopctl agent status <名称、地址或UUID>`，返回目标常态实例的忙闲、当前执行、队列、sleep、进程与会话状态；任务内调用按基础 Agent 的实际项目授权。服务端拒绝跨项目、平台及任务地址，不开放配置、历史或管理操作；外部身份保持原命令行为。内置 Skill 已同步用法。

- 全局顶栏入口为“收件箱、Skills 库、外部身份、插件、全局设置”；选中项目或平台范围时隐藏，点击 LOGO 返回全局后恢复。项目中的插件启用／配置入口、Agent 自身 Skills 与收件箱保留。五个入口的范围调整均已在本地部署；21 项导航定向、类型、局部 lint 与桌面 HTTP 夹具验证通过，证据在 `.local-build/global-tools-nav-ui/`。

- Agent 详情头部已在本地改为右上角紧凑名称、Manager 身份和小状态标签，去掉大圆圈；左侧地址／复制、页签及状态阶段、sleep 信息保留。名称粉色阴影已缩小到 1px × 1px，桌面夹具已核并部署。原头部 6 项状态定向检查、类型、局部 lint 和 1440 桌面 HTTP 夹具布局验证通过；预览在 `.local-build/agent-header-ui/`。旧完整 AgentDetail 用例另有凭证 fixture 缺接口失败，未扩修或宣称整文件全绿。

- 已取消本地 Pi 全局执行名额上限：默认及当前运行配置 `LOOP_NOW_PI_MAX_PROCESSES=0` 表示不限，显式正数仍可限制；同一执行实例的消息顺序与空闲回收保留。6 个 fakePi 同时执行、取消、显式限额兼容的定向与 race 检查通过；实际启动配置已读回确认。

- 普通对话的 Inbox 平台提醒已在本地改为紧凑一行：平台提醒、时间、真实发件人、截短内容摘要。依据执行中保存的原始 Inbox 消息 ID 只读查询，缺失时中性提示，不显示模型操作说明、不标记已读；sleep 提醒和任务原始正文保留。源码、合同、19 项 Web 定向及桌面夹具检查通过，部署后已核实际历史消息关联、摘要读取和页面资产。

- Agent 配置与新建入口的思考等级选择已在本地部署：只显示固定 Pi 版本中该模型支持的等级，默认按官方规则解析 `medium`，不支持推理的模型为 `off`。保存从常态下一轮生效，新任务捕获固定等级；已有任务快照不改。恢复默认时显式应用解析值并核对 Pi 实际状态，避免沿用会话中的旧等级或静默降级。迁移 024 已应用，原 Agent 保持默认；不增加单条消息覆盖或思考内容展示。

- 普通聊天输入框已居中限宽，图片加号与发送键放在框内；已修复自动链接误吞 Markdown 加粗符和中文尾句。导航采用项目概览，点击左上角 LOGO 返回未选项目的全局概览；顶栏收件箱仍是操作者 `@console` 的跨项目往来。本批已统一更新到本地服务。

- 专用浏览器改为官方插件 `playwright`（`plugins/official/playwright/`）：Pi 包，`loopNow.mcpServers.browser` 启动固定版本的官方 MCP 0.0.83 与预先准备的 Chromium，按 Agent 独立配置目录，任务副本并发时自动 `--isolated`；运行时不下载、不 npx。首次需用 Node 24 运行它的 `setup.mjs` 准备浏览器到 `<work root>/platform/plugins/playwright/data`（可用 APFS 克隆旧版 Chromium 省去下载）。

- 普通 Agent 对话与任务节点的图片输入，以及去掉页面背景点阵，已在本地启用。支持粘贴、拖入、选择、发送前预览／移除和历史放大；图片随消息保存并通过 Pi 图片参数交给模型，任务节点仍走 Inbox。首版仅 PNG／JPEG／WebP，默认每条最多 4 张、每张 5 MiB，限制可配；不扩展其他媒体。

- 插件系统第二版（规范 `plugins/SPEC.md`，协议 `api/plugin-host-protocol.md`）已实现，替代并拆除首版（旧表、旧宿主、旧页面、旧 Node 代码全部删除，数据不迁移）。一个插件是一个目录，`package.json` 里 `pi` 段按 Pi 标准声明 Agent 半边（扩展、Skill），`loopNow` 段声明平台半边（事件、权限、配置 Schema、账号、MCP 服务器、CLI、操作按钮、HTTP 入口、定时、渠道）；清单说了算，代码超出声明即加载失败。Agent 半边作为 Pi 扩展随授权挂进该 Agent 的 Pi（`-e`、`--skill`、合并后的 `mcp.json` 加 `-e builtin:mcp`、PATH、环境变量）；平台半边加载进一个共享的常驻 Node 宿主（stdio JSON 行协议，崩溃 5 分钟 3 次停下、可手动重启、全局一键停用）。事件分拦截／修改／认领／观察四种，观察类在业务事务内由触发器写入并至少送达一次；平台半边用 `lnp_` 令牌按声明范围调控制台 API。宿主提供持久状态、密钥（明文存库、不回显、只注入本插件）、配置表单生成；SDK `@loop-now/plugin-sdk` 提供类型和带重试的投递库（投递记录按约定键展示）。安装来源：官方（随平台发布）、本地目录、git、npm、市场索引仓库；先预览展示两半内容、事件和权限，非官方来源需确认；项目启用后按 Agent 授权。官方插件：`webhook`（收件箱有新消息即签名 POST，替代外部轮询）、`hello`（两半最小示例）、`playwright`（专用 Chromium，按 Agent 独立配置目录，一次性准备浏览器）。Pi 扩展的 confirm／select／input 在普通对话里转成对你的提问，任务回合里按取消处理（用户决定：任务里要人决定走收件箱提问）。插件可代管外部身份（规范 §12）：清单里渠道声明 `manageIdentities`、权限 `identities.manage`；外部身份页选代管插件（只限控制台）；发给被代管身份的消息和提问作为 `identity.inbound` 只送给代管插件；插件用 `asIdentityId` 以该身份回复或答题，效果与身份亲自回复一致，审计记代办；官方 `webhook` 是参考实现（推送带选项与回调地址，验签后替身份作答），飞书等渠道由插件作者照此实现。
- 插件授权规则：Agent 令牌没有管理权限，Agent 半边的权限等于该 Agent 的权限；插件只能给平台 Agent 或已启用它的项目里的 Agent 发消息；删除插件保留发件方历史；不做沙箱，信任靠来源分级、安装前展示和一键停用。
- 官方插件市场：索引仓库 `AI-for-anyone/loop-now-market`，服务端读取 `https://raw.githubusercontent.com/AI-for-anyone/loop-now-market/main/loop-now-market.json`（每次打开市场页和安装时现场拉取，不缓存）。收录即免确认安装，所以每条经 PR 审阅合并；git 条目一律指向 tag，仓库 CI 用本仓库的宿主校验每个条目。索引目前为空；服务端 `LOOP_NOW_PLUGIN_MARKET_URL` 的默认值就是这个地址（`config.DefaultPluginMarketURL`），设成别的地址即换市场，没有停用开关（用户决定，需要时再加）。每次打开市场页都现场拉取 GitHub，本机已出现过一次 TLS 握手超时，页面只在该市场条目上显示错误。
- 插件线索提问（迁移 033，2026-10-28）：Agent 在"插件作为发件方"的线索里回复时可带选项提问，`channel.outbound` 带 `isQuestion` 与 `question`（与 `identity.inbound` 同形），插件用 `inbox.send` 范围回复作答（SDK `api.channels.answer`）；插件自己不能发问（422），回复不属于自己的线索 403。宿主 load 与 `config_changed` 的 `config.projects` 只含启用了该插件的项目（SDK `api.config.projects()`）。首个用途是 Slack 插件（用户决定）：单独公开仓库 `AI-for-anyone/loop-now-plugin-slack`（0.1.0，2026-10-28 已作为官方市场第一条收录，git 来源钉在 tag v0.1.0；隔离测试 20 项对假 Slack 通过，用户已用真实 Slack 跑通），Slack 频道绑项目、私信走默认项目、Socket Mode 不需公网地址、渠道代管一个外部身份让 Agent 反向找人，范围就是启用了该插件的项目。

## 已交付与未完成

同项目 Agent 状态查询、三个全局导航入口按范围显示、Agent 详情紧凑头部已统一构建并在本地部署。状态查询的真实 CLI → 隔离 HTTP／TEST PG 权限与投影检查、定向 race、契约和 vet 通过，证据在 `.local-build/project-agent-status/`；两项界面的受影响定向、类型和局部 lint 检查通过。实际切换后已核对安装的 CLI 身份声明、匿名请求拒绝及页面资产，未发起新的模型或业务请求。额外全命令采样发现四项原有 `--run`／`--run-id` 同时传参的测试夹具冲突，保留失败日志，未扩大本次修复范围，也不宣称全 CLI 测试集通过。

取消 Pi 全局并发上限、普通聊天平台提醒摘要两项已统一部署。定向与构建检查通过，切换前执行和队列为空，未中断业务回复；桌面夹具预览在 `.local-build/inbox-reminder-ui/`，实际服务读回在本批发布目录。

思考等级选择的正式接口、保存与任务捕获、Pi 执行和配置页面已完成并部署。真实 TEST PG／HTTP、Pi 1.4 离线等级与恢复、任务冻结／旧快照兼容、准备阶段取消以及 Web 定向／桌面夹具检查通过；本次部署只读核对了实际模型等级、Agent 默认值和页面资产，没有发起真实模型调用。隔离验证在 `.local-build/thinking-level/` 与 `.local-build/thinking-level-ui/`。

居中输入框、Markdown 自动链接边界、项目概览与 LOGO 返回全局，以及 Playwright 插件与必要的有状态 MCP 接线已完成源码、必要定向检查和本地部署。导航检查另发现两项概览实时状态更新测试失败，本批未改其合并逻辑，不将整个概览测试集记为通过。新插件宿主的内置可用版本为 1.0.2，保留已有选择和任务版本。

2026-10-26 切换到 `loop-now-inline-inbox-20261026-1`（前后端，无迁移）：去掉操作者 @console 收件箱页面。Agent 发给你的消息与提问内联显示在其对话或任务节点对话中，可用选项按钮或"回复这个问题"作答，回答即已读；消息滚动进入视野才标为已读；Agent 列表与任务列表显示"待你回复"角标，概览顶部汇总"待你处理"；外部身份页可来回发消息；旧 `/inbox/<thread>` 链接按任务、Agent、身份跳转。新增 `GET /agents/{id}/console-messages`、`GET /inbox/pending`，Inbox 消息带 `task` 归属，节点对话包含该节点与 @console 的往来。隔离真实后端端到端 10 项通过。Agent 之间的 Inbox 不变。

2026-10-26 前端再切换到 `loop-now-composer-sleep-ui-20261026-1`（后端不变）：图片附件改为输入框内上方的缩略图条（普通对话与任务节点），上传状态显示在缩略图上；sleep 唤醒改为一行淡色系统事件（到期时间、睡眠时长、Agent 说明），不再显示 sleepId、关联 ID 和给模型的指令文本，提前唤醒在触发消息上标注。

2026-10-27 切换到 `loop-now-stats-nav-20261027-1`（前后端，无迁移）：左侧栏导航（方案 A4）上线，范围切换在左上角，项目内为概览、任务、项目文件夹、项目设置与 Agent 列表，底部固定插件市场、Skills 库、统计、外部身份、全局设置；项目设置与项目插件的"返回"链接去掉，进入项目 Agent 页直接打开 Manager 或第一个 Agent，手机宽度侧栏收进抽屉。新增 `GET /stats`（from、to、bucket=hour|day 必填，可按项目或平台筛选，只读聚合，无新表和索引）与统计页：今天按小时、近 7／30 天按天，图表为内联 SVG，有图例、悬停与键盘提示；等待时长按入队到开始回复计算；任务回合不记录工具调用，工具失败率只覆盖普通对话。存储测试在三个时区与 -race 下通过，HTTP 契约校验通过；Web 新增 9 项页面测试，全量 961 通过、9 项为既有失败；侧栏渲染导致一项配置页测试时序变化，只改了测试。截图在 `.local-build/nav-shell-ui/` 与 `.local-build/stats-ui/`。

2026-10-27 前端再切换到 `loop-now-chat-scroll-fix-20261027-1`（后端不变）：对话、任务节点对话和侧栏 Agent 列表的滚动区改为定位容器，未读点和待回复角标的隐藏读屏标签不再把整页撑长、把输入框顶到视口上方。

2026-10-27 切换到 `loop-now-loopctl-project-20261027-1`（前后端，无迁移）：loopctl 项目治理上线，`project create`、`project rename`、`project delete --yes`、`project manager set/clear` 只开放给外部身份，新建、改名、删除与 Manager 五个接口改为 `console-and-external`，校验与控制台一致；用户决定平台 Agent 不能建项目。

2026-10-28 切换到 `loop-now-plugins-v2-20261028-1`（前后端加插件宿主，迁移 031）：插件系统第二版上线，首版整体拆除。切换时删除了旧版宿主目录 `platform/plugins/host`，后端环境新增 `LOOP_NOW_PLUGIN_HOST_ENTRY` 与 `LOOP_NOW_PLUGIN_OFFICIAL_DIR`（指向发布目录内的 `plugins/host/main.js` 与 `plugins/official`），宿主由 `LOOP_NOW_NODE_BIN`（Node 24.21）拉起。核对项全部通过（宿主在切换核对时仍在启动，随后确认运行中 0.1.0；官方来源 hello、playwright、webhook 可见），无启动警告，切换前备份已留。隔离验收：Go 真宿主协议测试 6/6；Node 侧 51 项；Web 插件与对话测试 199 项；真后端加真宿主的端到端 10 步两次通过（安装 webhook、配置、项目启用、外部身份发消息触发签名投递、详情页、安装 hello、Agent 授权、下一轮 Pi 挂载扩展）。尚未在真实业务里安装任何插件；Playwright 的浏览器数据目录由主会话上线后准备。

2026-10-28 切换到 `loop-now-identity-manage-20261028-1`（前后端加插件包，迁移 032）：插件代管外部身份上线，节点对话列表补入节点与外部身份的往来。核对项全部通过，宿主运行中，无启动警告，切换前备份已留。隔离验收：真宿主 Go 测试 7/7（含代管）；Node 侧 56 项；真后端加真宿主的端到端 17 步通过，含 Agent 向外部身份提问与任务节点提问、回调作答、任务由等待恢复、节点对话显示身份回答。尚未在真实业务里绑定任何代管。

2026-10-28 再切换后端到 `loop-now-market-default-20261028-1`（只换服务端与 loopctl，无迁移，前端 5173 未动）：官方插件市场地址成为默认值。由用户亲自执行切换脚本（自动模式拦截了部署命令）；核对项全部通过，无启动警告，`GET /plugin-markets` 返回官方市场且无错误，切换前备份已留。注意：线上后端的插件宿主入口、官方插件目录和前端静态文件仍指向 `.local-build/loop-now-identity-manage-20261028-1/`，该目录在下次完整发布前不能删。同日仓库转移到组织 `AI-for-anyone`，历史压成单个"初始公开版本"提交并重打 v0.1.0。

2026-10-28 切换到 `loop-now-channel-questions-20261028-1`（前后端加插件宿主，迁移 033）：插件线索提问与宿主只列已启用项目上线；概览页实时状态修复（旧状态整条覆盖新状态，只有 sleep 变化才刷新）；Pi 崩溃错误现在等进程回收完再取 stderr 尾部；Web 测试集既有失败清零（6 条过时测试、1 条超时抖动），Go 测试集在 Linux CI 全部通过（前提缺失时跳过、3 处确定性等待、契约样例补 `market` 字段）。由用户执行切换脚本；核对项除官方市场外全部通过，无启动警告，切换前备份已留；官方市场核对当下遇到一次 TLS 握手超时，随后三次重试均正常，属网络抖动。插件宿主与前端静态文件改由本发布目录提供，`loop-now-identity-manage-20261028-1` 目录不再被线上引用。GitHub Actions 从本批起对每次推送跑三组检查，第五轮全绿。

2026-10-26 另单独切换前端到 `loop-now-agent-plugins-ui-20261026-1`（后端不变）：Agent 配置的插件权限改为紧凑列表，每个插件一行显示授权摘要与账号，一次展开一个编辑，权限／账号／资源每项一行、技术 ID 移入悬停提示；项目未启用的插件收在底部只读分组。

2026-10-26 切换到 `loop-now-credential-cleanup-20261026-1`（迁移 030），停服约 4 秒：删除单个账号时立即清空未被进行中任务使用的版本密钥（被已结束任务飞书绑定引用的保留为无密钥墓碑），进行中任务仍在用的版本记入 `credential_pending_wipes`，在最后一个使用它的任务结束或服务启动时清空；普通对话中正在用该账号的回复随即失效、不会复用旧密钥。删除 Agent 时清空其已结束任务快照中的模型与 CLI 凭证。删除项目、账号、Agent 共用一套清理逻辑。

2026-10-25 随后切换到 `loop-now-project-delete-fix-20261025-1`：修复删除项目被已结束任务的飞书绑定阻塞（被引用的 CLI 版本保留为不含密钥的墓碑，同时清空该项目任务快照中的明文凭证），无新迁移。之后按用户要求删除了 model 项目：项目账号、凭证与任务快照凭证已清空，常态 Agent 文件夹已从磁盘删除，仓库 `config/projects/model/` 已移除；随后按用户要求用一次性工具 `.local-build/purge-project/` 在单个事务内硬删除了该项目在数据库中的全部行（16,194 行及 83 个仅它引用的 blob，含与平台 Agent 联调巡检之间的 2 条跨项目消息及其触发的一轮执行、引用它的 350 条审计记录），删除前未另做备份，其他项目逐表计数不变；Redis 残留事件流已删除。

2026-10-25 本地已切换到 `loop-now-queue-folders-20261025-1`（含排队重做与 Agent 文件夹／上下文），停服约 4 秒；控制台 5173、服务端 8780、外部身份 TLS 8781。切换前回复与排队为零，1 个常态 sleep 不阻塞。切换前 `pg_dump` 备份，新服务沿用原环境启动并应用 028、029，原 0–27 账本行不变；账号行、任务快照、插件版本、对话、Agent、任务、产物与 Inbox 校验一致，启动无 WARN，页面资产、同版 loopctl 与用户终端的 `~/.local/bin/loopctl` 已核对。上下文目录为 `/Users/ljl/.loop_now`，按需创建。证据在 `.local-build/loop-now-queue-folders-20261025-1/deployment/`。同日早些时候已切换到 `loop-now-plaintext-queue-20261024-1`：用原主密钥一次性把 55 条凭证转为明文（模型账号 7、CLI 账号 14、旧凭证备份 2、任务快照 32），之后不再有主密钥，转换前备份在该发布目录。

运行记录中的 bash 已显示实际命令：新执行采集有界、敏感内容隐藏后的命令，普通／任务时间线共用；旧记录缺少命令时显示“未记录命令”，不扫描会话补造。普通对话利用主区域宽度，输入框居中限宽，无排队时不占侧栏，有排队时紧凑展示。相关定向测试及隔离宽屏浏览器检查已通过，本次切换前执行与队列均为空。

平台插件首版源码与必要隔离验收完成：安装、手动更新、项目启停／配置、Agent 授权、固定任务版本、Hook／转换、CLI 环境准备及普通 Pi 1.4 的标准 MCP 调用均已验证。内置插件共用生命周期，慢通知不阻塞人工取消；外部插件多账号加密保存，Agent 和插件动作显式选择，旧任务固定账号版本。管理页面和账号页面已通过真实 React → 临时 App／TEST PG／Node 插件联调。本次部署没有发起新的真实供应商调用、OAuth 或业务任务。

当前发布产物在 `.local-build/loop-now-credential-cleanup-20261026-1/`（排队与文件夹功能批次在 `.local-build/loop-now-queue-folders-20261025-1/`）：本机服务端、同版四平台 loopctl 和 Web；构建记录为 `build-result.json` 和 `web-build.json`，切换与读回为 `deployment/result.json`、`after.json`、`final.json`，备份为 `deployment/backup-before-030.dump`，更早的备份在各自发布目录。

图片与纯色背景的普通聊天、任务 Inbox、真实 Pi 离线图片传递与浏览器回看已验证；本地切换在全部执行自然空闲后完成，没有强停业务任务。内置插件随包可用版本已增加到 1.0.2，保留已选旧版本、旧账号及任务引用，不自动更新插件。图片功能尚未发起真实供应商模型验收。

仍未完整验收的方向：通用崩溃自动恢复、飞书群机器人、E2B、多实例和生产部署。飞书已有账号的跨用户重新登录策略仍待确认。它们不是当前自动待办，也不作为其他任务的默认前置。

## 本地开发与运行

依赖：Go 1.27、PostgreSQL 18、Redis 8、默认 Pi 1.4.0、Node 24.21.0。完整运行参数及默认值在 `internal/server/config/`，前端环境配置见 `web/.env.example`。

服务端和 loopctl 必须同版；在已配置数据库、Redis 及运行依赖的环境中：

```sh
go build -o bin/loop-server ./cmd/loop-server
version="$(bin/loop-server version)"
go run ./cmd/loopctl-dist -version "$version" -out "$PWD/.local-build/loopctl-dist"
LOOP_NOW_LOOPCTL_DIST_DIR="$PWD/.local-build/loopctl-dist" bin/loop-server
```

启动会执行缺失迁移并恢复平台状态，同一工作根只运行一个实例。已有本地服务更新时沿用原数据库、Redis 和工作根。飞书用户运行另需已审核的 authsidecar 发行包，通过 `LOOP_NOW_LARK_USER_BUNDLE_DIR` 配置；原 bot 使用官方 CLI 包。

```sh
cd web
./bin/npm24 ci
./bin/npm24 run dev
```

按改动选择必要检查：后端 `go test ./cmd/... ./internal/... ./api/...`；Web `./bin/npm24 run typecheck`、`run api:check`、`run lint`、`test`、`run build`。接口变化才运行 `run api:gen`，不手改生成类型。

GitHub Actions（`.github/workflows/ci.yml`）在推送 main 和 PR 时跑三组检查：Go vet 与全部 Go 测试（带 Postgres 18 与 Redis 8 服务，测试 Redis 固定用 DB 10）、Web 的 typecheck／api:check／lint／test／build、插件工作区的构建、测试与官方插件校验；Node 钉在 24.21.0 以满足 web 的 engines 约束。依赖本机 Pi、原生 helper 或 macOS 专有能力的测试在前提缺失时跳过，不算通过。

数据库集成测试使用 `LOOP_NOW_TEST_DATABASE_URL`／`LOOP_NOW_TEST_REDIS_URL` 指定的隔离库和前缀，缺配置的跳过不等于通过。浏览器与原生 CLI 测试需要各自夹具；临时产物留 `.local-build/`，凭证及运行数据不进入 Git。

## 文档维护

不维护独立的提案、规格增量、任务清单、签收矩阵或同步流程。根目录 `AGENTS.md` 只放简短工作规则，`README.md` 与 `README.zh-CN.md` 是面向公开读者的英文／中文介绍和上手入口，本文仍是唯一持续维护的内部概览。API、测试和业务 Skill 是程序工件，按需要维护。

旧规格、设计和验收文档已移除，公开仓库的历史从 2026-10-28 的"初始公开版本"提交开始；日常不加载旧规划，也不从历史任务列表自动续做。只有用户明确要求的新工作才进入执行范围。
