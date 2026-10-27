# authsidecar 独立实现与隔离证据（2026-10-20）

最新生产sandbox准入见 [独立收敛记录](sandbox-admission-verification.md)：精确地址门禁未通过，当前unsupported／failclosed。本文已绿native矩阵保留，不当新增生产准入通过。

本阶段授权限 `internal/server/larksidecar/` 与 `scripts/lark-user-variant/`。未改 OpenSpec checkbox、API、Store、项目账号或任务 schema；未操作现库／现有服务／真实账号／OAuth／QA／模型／Git。所有 token／用户／应用／消息均为合成值。依赖仅在构建时从公开源获取；运行验证只连自持 fixture。

## 当前可复用文件与工件

- `internal/server/larksidecar/broker.go`：每消费者独立私有 listener／随机能力，固定 Binding／TokenSource ABI，生产上游固定品牌 HTTPS、禁环境 proxy／redirect、单次 POST、资源限额与取消回收。
- `protocol.go`：官方 v1 raw-key canonical 验签、时效、最小 QA API 路径。
- `launcher.go`：已验证平台的强制网络隔离、私有当前 uid HOME、摘要准入、干净 user-only 环境及有界输入。原生文件／stdin／媒体入口的有限范围见 README；不能脱离 Command 裸启二进制。
- `*_test.go`：正式 broker／launcher 组件与真实 native 离线验证。
- `build.py`：发行阶段固定来源构建与平台包清单；不是运行机现场编译 installer。
- `body_fault_test.go.txt`：测试专用官方 middleware overlay，不包含在平台用户 CLI 中。

最终可复用工件在 `.local-build/lark-user-variant-proof/build-005/`：

| 内容 | SHA256／标识 |
|---|---|
| 官方源码 commit | f065bf5b645af381f9b7475ce721451e6ca36a23 |
| 固定源码归档 | 997aaddb0e74cc17903ee463c9bd6554ee671d7bdd5e0aa58343e74219ee46dd |
| 平台工件版本 | lark-cli-user-1.0.94-loop-authsidecar.1 |
| binary | 7158eb571a5c1eef593001c5a959f401cd688c858a84e9dc6abc7a789a87dbd1 |
| 平台 tar.gz 包 | b86f8ba6803406f954d922c008343ce920137bd7aa5a2b11c9071d70e9eed0e1 |
| 工具链／平台 | Go 1.27.1，CGO=0，darwin-arm64 |

manifest 明确 `officialRelease=false`、`sourceModified=false`、仅 `authsidecar` tag、平台版本 ldflags、官方源 URL、buildinfo／模块摘要及三个生产 Go 文件摘要。上述 binary／package SHA 是本机平台构建摘要，**不是官方发布摘要**。build-004 与 build-005 的 binary 摘要一致；005 增补最终平台源码清单，因此 ordinary 的004与最终005实际测试的是同一 binary。旧 bot 官方包与模板没有覆盖或重写。历史构建001–004和首失败记录保留，不当最终候选。

## 已验证矩阵与证据层级

| 格 | 实际验证与结论 | 证据 |
|---|---|---|
| v1 真接线／双 HOME／两波过期 | 两个真实 tag binary 消费者、相同可信 Binding、独立 HOME／随机能力；各3页，共6消息请求＋2次原生 user_info；初代 token 有效，两次分页屏障分别标合成过期，每波共享 Source 仅刷新1次，实际 HTTPS fixture 收到相应新代际 | ordinary-release.log；race-release.log 的 TestNativeTwoHomesTwoExpiryPagination |
| 权限／秘密隔离 | 私有 user-only env 不含真实 token／refresh／appSecret；HOME 预置另一个品牌／应用的合成 fallback trap，真实请求仍使用捕获 Source；输出与新增 HOME 文件未出现合成 token；trap 原文件不冒称新增泄漏 | 同上，fixture 只读写自持 HOME |
| 每进程网络拒绝 | 每个 native 运行前，精确生产 sandbox profile 允许指定自持端口，另一自持端口明确子进程 exit42；未探测外部地址／DNS，未运行真实 OAuth | native 的 sandboxGate；任一失败终止，非本平台不降级 |
| v1 篡改与 raw key | method／host／path／raw query／body／identity／authHeader／version／timestamp／重复头／target userinfo与query／管理路径／错误hexDecode逐格403，Source与上游0 | TestV1SignatureScopeReplay |
| 实际 native 签名重放／错 scope | 测试自持 front 捕获原生 POST 合成 v1 frame；同scope原签名重放409、业务转发仍1；换另一broker403且其Source0 | TestNativeCapturedSignatureReplay；captured-replay-second.log（race） |
| 去重与容量 | 修改签名进入 Source／上游前登记，重放不再发；同秒同请求的确定性限制保留；活记录满不淘汰而503；GET同秒可继续 | TestV1SignatureScopeReplay／TestScopeLifecycleAndCapacity |
| POST 未知结果 | 原生 POST 成功只发1次；上游接收后断连接，原生失败、实际业务计数仍1；不自动重签／刷新后重发 | TestNativeRequestFailureMatrix/unknownPost；组件对应格 |
| redirect | 组件301/302/303/307/308均拒绝；原生同host降HTTP和跨host Location 的四码增量证据见下节，不将Location／私有签名头返CLI | TestLimitsRedirectAndUnknownWrite；native增量 |
| 大请求／有限输入 | 普通 Content-Length／chunked 超限413且Source0；生产 launcher 拒2MiB JSON文件、拒无限stdin／multipart；合成 native stress 明确绕过输入准入但保留网络拒绝，证明官方先预读后 broker 413且业务上游0，不冒充官方 sender 已有内存硬限额 | TestBoundedInputSnapshot；native largeBody；TestLimitsRedirectAndUnknownWrite |
| 读 body 错误 | broker截断输入Source0；另用未修改的官方生产 middleware＋测试overlay注入Reader错误，对照实际到自持原目标1次且仅sentinel；精确生产 profile 阻断第二次原目标请求 | body-fault-first.log；TestOfficialMiddlewareReadErrorConfinement；未声称该底层Reader注入是普通CLI命令 |
| 响应／stream | response超限流不能完整成功；截断原生响应失败；空闲超时、客户端取消、broker退出均取消上游并有界释放；并发超限503 | TestResponseBoundsAndCancellation、TestScopeLifecycleAndCapacity、native cancel／truncatedResponse |
| launcher失效 | 摘要错误／缺坏proxy／管理命令／profile／bot参数拒绝；真实CLI遇broker失联失败且业务转发0，无直连／宿主 fallback 成功 | TestNativeLauncherAndFailureGates |

## 检查结果及失败历史

- `ordinary-release.log`：最终生产源码，完整本包 ordinary **PASS 2.701s**，native／middleware探针均显式提供，0 skip；所测004与最终005 binary SHA相同。
- `race-release.log`：完整本包 **PASS 15.202s**，0 skip，包含实际分页／取消／已捕获签名重放及官方middleware故障注入；没有 DATA RACE。
- `go vet ./internal/server/larksidecar` 退出0；build.py 在内存 compile 通过，无 pyc。
- `ordinary-first.log` 是较早阶段通过证据，不能替代最终源码。更早未落盘到日志的首轮 stream 测试暴露未flush响应头，出现 EOF；已在真实 broker flush 接缝修复，再以完整 ordinary/race 验证，不称首轮全部通过。
- `captured-replay-first.log` 保留 missing `net/url` import 编译失败；第二轮定向 race 通过，不覆盖首失败。
- 独立 overlay 第一次命令因错误 cwd 未创建源文件／映射而失败，随后在正确仓库路径生成，原固定官方源码未写入／修改。运行证据与路径如上，不把编译命令成功当业务调用通过。
- 最终源码摘要：`.local-build/lark-user-variant-proof/source-sha256.json`；生产源码与005 manifest逐文件复核一致。

## 仍未完成／server 接线责任

1. 本阶段共享刷新 Source 是带锁的合成组件，不是 PG。server 必须用捕获 Binding 适配正式同版本 PG lease/generation协调器，证明持久提交后才转发、等待取消不丢已开始轮换、响应丢失／提交未知失败关闭。不得据本包绿勾整个0.2／2.3／3.3。
2. 正式 installer／normal-task捕获／wrapper入口／父进程退出治理／App 尚未装配。本组件只提供 Command 与 Close，不证明Pi子进程树／任务重启／旧current隔离已完成。必须保证调用方不绕过强制launcher、不继承env，并负责终止和Wait后Close。
3. 当前支持平台只实测 darwin-arm64；其他平台不得以原官方包／宿主工具／临时编译回退。真实供应商、用户授权、QA资源访问未执行，四scope不覆盖所有shortcut。
4. 旧bot原安装路径与官方包未改；本轮未重新运行旧bot真实请求、不把源文件未变当旧bot完整回归。
5. 本轮未读取／修改当前业务task、API／Store／数据库／现有服务；未进行迁移、部署、真实认证或Git。

## 资源清理

所有组件／原生测试使用自己的随机loopback监听、httptest TLS、私有TempDir及有界context；test Cleanup／defer关闭broker／front／TLS服务、Wait所有native子进程并回收TempDir。未使用现有PG／Redis或4318/4320。未创建DB schema或临时系统配置。仅保留专用proof目录中的工件、公开源码、合成overlay与安全日志供server复用。

## 最后定向增量与冻结

`native-redirect-final-race.log`：只扩展既有 native redirect 格为301/302/307/308，交替同host降HTTP及跨host Location，全部实际 binary 失败且业务转发各1，**PASS 11.609s，0 skip／race告警**。未知POST／大body／截断／取消在同一必要定向slice再次通过；没有改生产源码或扩大业务范围。全包先前15.202s证据与该最后增量分别保留，不把两轮混作一次全包运行。

现有八份 Go/脚本/故障fixture源文件摘要已冻结在 `source-sha256.json`；生产三文件与最终005 manifest一致。后续若server集成发现确切ABI或审查缺口，再仅定向修改/复验。没有勾整个0.2或其他整项任务。

## 0.2 增量收口（2026-10-20，保持既定 ABI）

Main/server确认 `clicredential.LarkBinding` 的 OpenID 映射本包 UserID，RuntimeID由运行scope捕获；本包 `TokenSource.AccessToken`／`Start`／`Command` 不改名或改签名，未改其types／020／021。以下只补原生门禁和一项已复现的输入修复，未重复原两HOME／两波分页测试，也不把原合成Source换称PG。

### 真实 CLI 新增格

| 格 | 实际结论 |
|---|---|
| response声明长度／chunked超过1024字节 | 固定平台binary实际失败，每次业务转发仅1；没有完整成功或重试 |
| idle／total deadline | 实际慢流失败，上游context及时结束；total格持续小块flush，验证不是仅靠idle触发 |
| consumer被kill／Broker.Close／scope context结束 | 只杀自持CLI或结束自持scope，实际命令有界失败、上游释放，每格业务请求1；不是整个App父进程SIGKILL验证 |
| 缺proxy／坏proxy／缺key／错key／缺AppID／非法strict／非法default | 在测试中故意改变原生子进程合成env，实际失败且Source=0／上游=0，无超时冒充失败、能力不进错误输出；生产Command不暴露这些变更开关 |
| strict改成bot | 官方ResolveAccount先做UAT user_info（固定源码credential_provider.go197、225）；精确允许仅1次捕获用户userinfo、校验其UAT，随后CLI失败，业务0、不切TAT/bot/宿主。不能把这次合法bootstrap误写为Source=0 |
| 原官方bot工件 | 固定官方darwin-arm64归档SHA复核通过；从归档只解出binary到私有TempDir，版本1.0.94、bot-only原生命令dry-run成功；误带user-sidecar env实际exit2。没有真实token、OAuth／bot API／QA调用，不替代完整旧bot回归 |
| 无sentinel路径 | 测试专用official-route-fault-probe编译固定官方middleware＋overlay；自持原目标对照收到1个无认证请求，精确生产profile下0个新增请求、Source0。它不是正常CLI命令注入，不新增生产原始URL或fixture开关 |
| 实际预读观测 | 真实CLI使用32KiB JSON快照；Command后把原文件扩大至2MiB不改变已捕获输入，业务请求1。ordinary峰值RSS 38,420,480字节，race轮子进程38,404,096字节（darwin ru_maxrss），仅fixture观测，不声称生产RLIMIT或任意SDK流式内存有界 |

### 已复现且已修输入 FIFO 阻塞

`boundedInputs` 原来先阻塞式open、后Stat；自持FIFO让它在校验前等待写端。`input-fifo-first.log`保留真实失败；测试专门打开该自持FIFO写端安全解除等待，未遗留goroutine。最小修复只在本包launcher：非阻塞、拒跟随leaf symlink打开，再按同一fd确认普通文件并有界读取；不改官方源码／binary／协议／ABI。`input-fifo-second-race.log` **PASS 3.035s**；包括原普通JSON快照与FIFO立即拒绝。不是平台或供应商漏洞归因。

### 本轮证据与首失败

- `native-increment-first.log`：7个实际stream／退出格通过；provider strict=bot格因“所有上游必须0”的错误测试预期首失败。随后固定源码及合法userinfo实际字段确认，改为“只有该捕获用户userinfo恰好1、业务0、CLI明确失败”的更精确断言；`native-provider-second.log` **PASS 2.615s**。未改生产协议或放行bot。
- `native-official-bot-first.log` **PASS 3.671s**；仅版本／bot dry-run／误工件拒绝，不走CONNECT／CA路线。
- `native-input-route-first.log` **PASS 2.006s**；包含无sentinel middleware对照／拒绝与实际输入快照/RSS。
- `native-02-increment-race.log` **PASS 28.130s、0 skip、0 DATA RACE**；只复跑本轮新增stream／provider／bot发行边界／no-sentinel／输入资源格及输入修复回归，不把它称全仓或完整PG矩阵。
- `go vet ./internal/server/larksidecar`退出0。
- 所有新测试各自Wait子进程、defer关闭随机listener／broker／TLS／目标服务、TempDir自动清理；FIFO首失败也有专门解除和句柄关闭。无现库／Redis／现有服务或固定App端口操作。

### 最新可复用工件及冻结

FIFO修复改变平台launcher源码摘要，因此新建 **build-006**，没有覆盖005。固定官方源码与仅tag CLI binary本身不变，平台variant标识仍为 `lark-cli-user-1.0.94-loop-authsidecar.1`（尚未发布／部署）。

- 当前目录：`.local-build/lark-user-variant-proof/build-006/`。
- binary SHA256仍为 `7158eb571a5c1eef593001c5a959f401cd688c858a84e9dc6abc7a789a87dbd1`。
- tar.gz包SHA256更新为 `8ecdbb34397b25856f11e5ad1808a0136955de395f443e9e8edb772cacab339c`，因manifest记录了新launcher源码摘要；这是平台构建摘要，不是官方发布值。installer接006最新清单，不能把005清单说成修复后的源码。
- `native-02-increment-sha256.json`记录本轮9份Go／脚本／overlay源文件摘要；原 `source-sha256.json` 保留上一阶段8份冻结历史。
- 生产三文件已与006 manifest逐项核对一致；本轮未改API／Store／任务schema／OpenSpec checkbox。

后续最短接线仍是server以确定typed binding闭包适配PG Coordinator，再将 `Broker.Command` 接normal/task受控入口、在进程Wait/释放后Close。此前两HOME两波刷新证据是合成Source，PG加密commit可见／租约／取消独立持久化及App父退出仍按server正式集成验收；本轮不勾完整0.2、2.3或3.3。
