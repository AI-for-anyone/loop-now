# 认证桥独立组件验证

2026-10-14，主工作区 auth-helper/；使用 add-console-codex-auth 的 OpenSpec apply。Main 已独立核对并勾 1.1；本目录仅补安装包/辅助进程及实际 Pi 扩展证据，不编辑共享 change/tasks、Go server/API 或旧 plugin worktree。

## 固定基线与实际 API

- Node `/Users/ljl/.local/node-v24.21.0/bin/node`；实际安装 coding-agent/pi-ai 均 1.1.0，根 `/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent`，不是本地 Pi 源码 checkout。
- 包 ./oauth 导出为空，公开 provider 工厂是 `dist/providers/openai-codex.js:openaiCodexProvider`，OAuth login(interaction, options)/refresh(credential, signal)/toAuth 可用；模型须调用 getModels()，实测 9 项，不能读 models 属性。
- login 首先 select browser/device_code，本组件固定 browser；AuthInteraction 支持 notify、prompt 及全局/单 prompt AbortSignal。回调固定127.0.0.1:1455/auth/callback，Pi 校验 state/PKCE。Pi 吞监听错误后退 manual_code，本组件独立进程内 guard node:http 监听错误与清理，额外预检不作为 TOCTOU 保证。
- canonical credential 为 type=oauth/access/refresh/expires/accountId，expires Unix毫秒；结果过滤为固定字段且 JSON<=64KiB。手工输入只接受当前state匹配的完整回调URL，Pi原生允许裸code的分支不直接暴露。
- Pi CLI明确允许 --no-extensions 配合显式 -e；平台扩展覆写现有provider字段时 ModelRuntime 合并配置，保留未覆盖字段。实际双进程RPC探针确认OAuth覆盖位于真实请求认证路径，不只调用自定义函数。

## 完成组件

- helper/main/ipc：一进程一操作，fd3输入/fd4秘密输出，stdin不参与；1MiB JSONL与32条待写上限、非法UTF-8/JSON拒绝；stdout/stderr由Go启动器丢弃，provider原始进度/异常/body不进入状态/日志。仅授权URL和固定提示通过普通notify；凭证只通过secret result。
- Go process.Command：空环境、独立0700 HOME/tmp、固定基础PATH、production，绝对Node/entry，不注入主密钥/模型key，不加载AuthStorage/Agent Skills/插件宿主；操作目录/管道回收由server拥有。DrainTimeout可配置独立输出排空预算，默认5秒，终态写回堵塞也有界退出。
- helper refresh：started 后普通abort/EOF继续等待新凭证；协议故障也保留结果并标protocolFault、停止读取输入。预算到期明确refresh_uncertain，不声称未执行/旧refresh可重试。Go协调器必须用独立受限context加密回写，再处理等待者取消；不可交付结果/进程崩溃仍需中央inflight恢复。
- runtime-refresh/refresh-client：直接Pi RPC stdin/stdout不变，额外私有fd3/4，帧仅id/method=refresh/provider，不带可自填grant/Agent/task/token。每Pi由父进程绑定授权版本。独立32pending/32未排空写入及drain预算，超时/断线明确失败、不挂轮次、不把超时请求无限积压；取消仅撤销本地等待，迟到响应忽略。返回账号与启动账号不符则失败，不能回退供应商刷新或换账号。

## 离线证据

Node 17个顶层测试分批通过，无skip：helper 7、IPC/transport 8、实际Pi RPC 1、Anthropic resolver能力核对1。最近定向命令：

```sh
/Users/ljl/.local/node-v24.21.0/bin/node --test auth-helper/test/helper.test.mjs auth-helper/test/ipc.test.mjs
/Users/ljl/.local/node-v24.21.0/bin/node --test auth-helper/test/rpc.test.mjs
/Users/ljl/.local/node-v24.21.0/bin/node --test auth-helper/test/helper.test.mjs auth-helper/test/anthropic-compat.test.mjs
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -race -count=1 -cover ./auth-helper
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go vet ./auth-helper
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go build ./auth-helper
```

Go 2项顶层测试通过，96.6% statements；只测试本目录，不重跑其他会话的server/数据库测试。change strict 通过。

- **实际安装 Pi OAuth 登录入口**：仅global fetch离线stub（不发认证端点请求）；真实回环回调验证错state=400且不兑换、正确state成功、取消/超时关闭监听、端口已占用失败、预检后到Pi监听前另占1455的竞态被guard识别，无manual退化成功。未结束任何占用程序，所有临时监听仅测试自己持有。
- **实际安装 Pi RPC 请求路径**：两个直接CLI进程，--mode rpc --no-extensions加两个显式固定扩展（生产刷新扩展与仅测试的模型/工具fixture），无自动发现Agent资源。每进程实际完成模型→工具→同轮后续模型；两次过期波均经私有fd委托，内存模拟中央协调器每波只轮换一次，共2次。各自auth.json更新到第二轮且0600，RPC/日志没有合成token。
- **网络保护**：实际CLI在初始化之前通过 Node --import 加载test/deny-network.mjs，拒绝除本机fixture gate外所有fetch，并断言两进程供应商请求尝试=0。PI_OFFLINE不是本证明的网络隔离依据。实际模型流为fixture，不能称真实模型调用成功。
- **故障与秘密**：假provider成功/失败，prompt state/迟到/单prompt取消，login超时和迟到结果；started refresh普通取消、坏帧/第二命令/EOF仍交付唯一轮换结果，超时uncertain；输入坏JSON/标量/非法UTF-8/超限；输出不读的子进程有界退出；runtime多批超时写队列有界、失联/乱序id/迟到响应拒绝；秘密哨兵不进入普通通知、stdout/stderr或原始错误。
- watch H1/H2施工期静态提醒已对应上述局部实现与新增定向证据；watch只做静态复核，不把其报告写成独立测试通过。早期fixture曾误用models属性、CJS resolve不支持import-only exports和macOS cwd别名，均在本目录修正后重新验证。

## refresh-acceptance 覆盖与保留门禁

| 项 | 本组件证明 | 仍需server/整合证明 |
|---|---|---|
| 1 授权版本键 | 管道frame无版本选择，父进程绑定；返回accountId不变 | PG真实grant/version、同账号两次登录和旧任务绑定 |
| 2 多进程轮换 | 两个实际Pi进程、模拟协调器两波各一次 | PG单写、密文最新版本、禁止旧token回滚 |
| 3 长轮次 | 同一实际RPC轮次模型→工具→模型两次认证委托 | 真实模型长轮次/业务任务另外验收 |
| 4 刷新与登录竞态 | helper取消/迟到不出第二结果 | 原授权更新、新登录指针及事务并发 |
| 5 中断/丢响应 | 取消保留结果、超时uncertain、失联/drain有界 | durable inflight、响应丢失/PG写失败后的恢复，不可盲重试 |
| 6 原子回写/文件 | 启动隔离、scope文件0600 | PG信封原子回写、server初始文件原子替换及运行副本恢复 |
| 7 身份拒绝 | 不发grant/Agent/task选择，只走私有管道 | Go进程身份绑定/撤销、console-only权限与其他身份拒绝 |
| 8 故障与秘密 | 离线fake、实际Pi加载与secret sentinel | server数据库/重启/旧任务A新任务B完整矩阵 |

重要上游边界：安装包 AuthStorage.withLockAsync 使用锁内 writeFileSync 回写原文件，**不是原子rename**。已向server/Main报告；不猴补Pi文件存储。服务端初始落盘、中央已提交授权与重建/明确失败策略须另证，不能把本地scope文件复制称作跨进程刷新协调。供应商已轮换但结果丢失/不可加密回写的场景需要中央不确定状态，不自动换Agent新账号。

Anthropic能力附证只证明在每调用内存AuthContext中映射3种已知旧字段可调用原resolver，保持AUTH_TOKEN Bearer > OAUTH_TOKEN apiKey > API_KEY apiKey；没有实现或证明实际Anthropic运行扩展/凭证迁移。

本阶段不改1.1/2.1任务勾选，交由Main按共同证据确认。没有真实登录、认证端点/模型调用、下载、数据库写入、旧服务操作、Git或旧plugin worktree写入；全部实现/测试/协议/证据只写auth-helper/。

## 实际安装文件SHA-256

- `package.json`: `c662a36f8bc47843393a60dc8616a34dfcc8376e8fcc96dbc2f1020c60e38e2a`
- `node_modules/@earendil-works/pi-ai/package.json`: `a8f10d5157b335379ae00edf9c25ae7a15bfc0f3a76107d4376a3779bb5b2995`
- `node_modules/@earendil-works/pi-ai/dist/providers/openai-codex.js`: `44087142be22adfd09cf1a87edbbdb74ce155b5fd34e70fa4666f3a9f8a9a680`
- `node_modules/@earendil-works/pi-ai/dist/auth/oauth/openai-codex.js`: `0b94a219ddb5982f0ee543ef03138ed0254ce6ee0a57c9d89d39c242e2eaf6f6`
- `node_modules/@earendil-works/pi-ai/dist/auth/oauth/callback-server.js`: `2dda468f4937edcf7bb5e8528de4bf7753efbe5b48610a3f9ea47ff2c4908ede`
- `node_modules/@earendil-works/pi-ai/dist/auth/resolve.js`: `3fcec09018831324d9c7fb02f4aea707e8a9e4fec5a2bdf1b3d84d716fd3091e`
- `dist/core/auth-storage.js`: `0b45029901579032b19273a1427f63b622df8e1aaf9ea185eb932c0d4898998c`
- `dist/core/provider-composer.js`: `b088d1babb75360da6bf1bcbe8e140ce600e1db15f51345f1965d3d1e9f267ef`

## 后续独立块：嵌入部署与 Anthropic 待验证（2026-10-14）

- 新增 assets.go/assets_test.go，embed根目录所有*.mjs（不含test）。Install(root)返回固定main.mjs/runtime-refresh.mjs；逐层拒绝符号链接并核对打开后的目录身份，root及直接父目录0700、文件0600，随机独占stage写入/Sync/Close/Rename逐文件原子替换；不删除无关文件，不修改Agent目录或SDK。
- `go test -race -count=1 ./auth-helper -run TestInstall` 3项通过；覆盖嵌入内容一致、固定返回路径、打开的旧inode不被原地改写、无关文件保留、stage回收、符号链接/非普通文件/公开目录拒绝与并发同版本安装。`go vet ./auth-helper`通过。server已接Install入口，不据此称实际服务已替换。
- server随后请求legacy-anthropic.mjs：只在明确的旧credential.env场景显式加载，用实际安装1.1.0原apiKey.resolve、每调用内存ctx映射AUTH_TOKEN/OAUTH_TOKEN/API_KEY，未知项拒绝，不修改process.env或runtime-refresh。纯resolver5种合成矩阵已通过，但新native客户端实际Pi探针未通过。
- **隔离事故与暂停**：新Anthropic native探针两轮调试失败，已观察到真实供应商401 Invalid bearer token；凭证仅合成，没有读取用户凭证。global fetch preload没有拦住原生请求，不能称本测试零外连或成功。已向Main/server如实报告，按用户指令停跑并固定skip，防止全量测试误触发。既有Codex假stream模型/双进程私有fd证据单独保留。
- 可审查方案见 anthropic-isolation-plan.md：固定本机端点、get_state最终URL前置断言、进程级network deny（当前仅确认sandbox-exec存在，未验证可用），先经Main核对再验证隔离能力，能力未通过不运行native SDK探针。不能仅加JS patch重试外部地址。

## 仅隔离能力与 get_state-only 核对（2026-10-14）

Main批准隔离能力验证后，在两个测试自持127.0.0.1端口进行本机探针：未加sandbox时都200；实际profile `(deny network*)` 仅放开 `(remote ip "localhost:52679")` 后，允许端口200/1次请求、拒绝端口52681返回EPERM/0请求，exit0，stderr空。最初IP字面量profile被解析器拒绝exit65（子Node未执行），已保留失败profile。结果及profile见sandbox-capability-result.json。

在同形已验证sandbox内，仅启动实际Pi1.1.0并发送get_state；独立models.json设置本次本机fixtureURL，返回model.provider=anthropic、api=anthropic-messages、id=claude-fable-5、baseUrl=http://127.0.0.1:54186，精确匹配；允许/拒绝fixture均0请求、stderr空。未发prompt、未调用native模型客户端、未探测外部地址。metadata目录第一次fixture解析出错发生在Pi启动前，修正记录保存在anthropic-state-result.json。所有测试资源已回收。

native test.skip仍固定保留，旧native body不可直接启用；Main核对后还需先改本机endpoint+sandbox+最终URL断言才可允许native模型请求。这里不是Anthropic native兼容验收，也不把此前两轮意外401改记为成功。没有Git、系统配置修改或宿主凭证读取。

## 重构 native SDK 本机探针通过（2026-10-14，Main明确放行后）

完整替换原危险test body，未直接启用HTTPS/preload方案。`/Users/ljl/.local/node-v24.21.0/bin/node --test auth-helper/test/anthropic-rpc.test.mjs`：1顶层测试、6case、无skip，通过。每次case用新自持随机回环端口，先sandbox允许目标200/拒绝另一端口EPERM，再同一profile从CLI初始化前保护实际Pi1.1.0；仅get_state核对provider/api/model与精确loopback baseUrl后发合成prompt。任何沙盒/preflight/URL失败直接fail停止，不降级。

原生Anthropic客户端未被替换，fixture接收实际POST `/v1/messages?beta=true`、核对Host/认证模式并返回静态SSE；每case模型请求1次、非许可端口0次，SDK成功解析回复。6case为AUTH_TOKEN单项、OAUTH_TOKEN单项、API_KEY单项、三项AUTH优先、两项OAUTH优先，以及sk-ant-oat合成前缀由原生API识别为Bearer。进程环境没有这些模型字段，RPC/stderr没有凭证值；不记录实际headers/body。结果存anthropic-native-result.json。所有自持资源已回收。

本证明仅覆盖已知三字段的实际Pi认证解析/本机原生SDK路径。proxy/未知变量、PG版本/任务回放、真实账号/真实模型仍未验证，不因此勾整项兼容或真实验收。此前两轮意外401按失败历史保留；新的本机结果不将旧事故改记为离线成功。

## 本目录最终合并检查（2026-10-14）

暂停前已完成以下检查；恢复后仅补记结果，没有重复运行：

```sh
/Users/ljl/.local/node-v24.21.0/bin/node --test auth-helper/test/*.test.mjs
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -race -count=1 -cover ./auth-helper
```

Node 共18项通过，0失败、0跳过，包含重构后的六场景 Anthropic 原生 SDK 本机探针、实际 Pi Codex 双进程委托刷新与实际 Pi OAuth 入口离线 stub。Go 共5项顶层测试通过，当前整个 auth-helper 包覆盖率77.7%；前文96.6%是新增 assets.go 前的进程构造器阶段统计，不能作为当前包覆盖率。

组件验证已收口；中央 PG 授权版本/加密回写/丢响应恢复、执行器完整接入及真实登录/模型验收仍属于 server/Main 的整合门禁，没有由本目录测试替代。保留两轮意外401历史及各阶段隔离边界。没有重复运行数据库/server检查，没有写 DEV、操作旧服务或 Git。
# 2026-10-14 callback成功后失败热修复（独立离线证据）

本轮只恢复已有Codex认证故障排查，插件全停。Main批准两个已复现的固定Pi1.1.0适配缺口；机制见compatibility.md，活动design由server独占引用。实际安装lazyOAuth透传，callback-server先返回成功页，Codex后exchange再从JWT取accountId；成功页不代表平台保存成功。

修改前，实际冷lazyOAuth + runIPC + fd3/4合成exchange验证5场景通过（此处“通过”指复现预期）：小数expires_in导致JS成功帧含小数expires；合法base64url含-_导致原Pi atob失败。正常响应完整flush退出0；401和运输异常均可在callback200后failed。没有用户token/code被读取，也没有真实OAuth/模型请求。这两条可发生路径不是用户此次根因定论。

修改后必要定向命令：`/Users/ljl/.local/node-v24.21.0/bin/node --test --test-concurrency=1 auth-helper/test/codex-login.test.mjs auth-helper/test/pi-oauth-scope.test.mjs auth-helper/test/helper.test.mjs auth-helper/test/ipc.test.mjs`，26项通过、0失败、0skip，1.773s。新实际SDK子进程8场景：正常标准base64、小数expires、base64url、401、运输异常、非法JSON、缺accountId、同账号refresh。覆盖fd4终态恰好一次/退出0、stdout/stderr为空、安全诊断与无秘密错误；独立scope测试验证标准base64/非法输入、finally恢复和expires安全范围/向下取整。既有取消/超时、端口TOCTOU、背压/迟到检查保留通过。首次并行合跑20通过1失败，原因是固定1455由两个测试竞争；串行定向消除测试资源争用，未改任何现有服务。

新SDK fixture从CLI/helper初始化开始置于macOS sandbox：`(version 1) (allow default) (deny network*) (allow network-bind (local ip "localhost:1455")) (allow network-inbound (local ip "localhost:1455"))`。只有回调入站绑定允许；fetch由测试fixture直接返回合成Response且不调用原fetch，固定请求形状断言，不通过外部地址试探保护，不代理/重定向。sandbox/ready不成功则测试失败，无降级；临时HOME与自持子进程结束后清理。

源文件收口：helper.mjs/pi-provider.mjs/pi-oauth-scope.mjs、test/codex-login-fixture.mjs、test/codex-login.test.mjs、test/pi-oauth-scope.test.mjs及本组件protocol/compatibility/verification。Go和active design由server另报，Main负责构建与服务切换。本轮不改服务、DEV或Git，不勾5.3。真实单次重试须靠新安全stage区分此次实际失败路径。
