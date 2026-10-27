# 权威 native manager 与实际 PATH 接线

2026-10-20。遵循当前 ownership revision 9 与活动design「真实PATH与双loopback接缝」。本记录替代010描述符直exec作为当前集成入口；旧记录/工件保留历史，不再用于runtime装配。

最新不改source/pins的既有原Manager真实PATH/RPC定向复核：ordinary4.990s、race3.217s、各1项零skip，见 [native-rpc-current-verification.md](native-rpc-current-verification.md)。不是010正例，也不外推完整Pi/App或真实PG/provider验收。

随后App隔离门禁需要跨包HTTP fixture，已最小增加原Manager构造期`NewManagerWithOptions(ManagerOptions{HTTPTransport})`，默认NewManager仍nil生产HTTPS/Proxy=nil。真实PATH/RPC定向改走公开构造后ordinary2.175s、race3.232s、各1项零skip及vet0，见 [manager-transport-options-verification.md](manager-transport-options-verification.md)。只source增量，不重建014或改pin。

本轮再核013的旧sealMu短ctx报告：当前生产源码已为`sealToken`可取消串行准入，取得后复核ctx及released/sealed状态，无需再次修改。只把既有`TestConcurrentSealWaitHonorsOwnDeadline`改回实际原Manager/AcquireRuntimeGate，消除其对未发布facade的依赖；真实双listener/最终连接退出屏障证明首次Seal仍在drain时第二30ms deadline及时退出，Release不能破坏未确认gate，放开屏障后首次正常完成。该单项ordinary **0.844s**、race **1.983s**、零skip；日志与源码SHA在`.local-build/lark-user-variant-proof/seal-current-manager-final/`。生产manager SHA仍`3114a79f493102a191e650083112182a5db582f873c2f9245a1e2f5fb72e3480`，本轮只有测试/本记录变更，无新manifest/pin/native编译或App/native矩阵重跑。既有013/014及首失败记录保留原范围，不将该局部测试外推App/PG/真实native。

## 当前确定内部 ABI（保留原Manager入口）

Main最新revision12撤销facade迁移派工，继续下列已接通的 `NewManager/OpenScope/ManagedScope.Close()+Wait(ctx)/AcquireRuntimeGate/StopIdle(ctx)/Release()`。不改变App/local/task接口，不新增manager或更换pins。此前facade源码、测试及 [未发布说明](native-manager-abi-verification.md)只保dirty历史，不能作为产品入口或已集成证据。

`Close()`保持error返回并与Broker错误一致；`Wait(ctx)`才确认排空。反复StopIdle与Release保持幂等。Release的最终区分是：尚未开始Seal可以纯内存回滚gate；已开始Seal但未成功必须保gate，不能因Release伪造排空。Main已明确撤回“一律未sealed拒释放”候选，以保native Acquire成功而local Acquire busy时的安全撤销路径，见 [本轮定向证据](gate-release-verification.md)。

**014及其App/race证据不变，未生成新包、未更改安装pin或部署。本轮未发布facade及Wait取消复核增量不冒称已包含于014；只由Main/server统一后续审查。**

## 既有已集成 ABI

```go
m := larksidecar.NewManager() // App只创建一个，共用于factory和task group fence
scope, err := m.OpenScope(ctx, binding, source, cfg, limits)
// scope: BinDir() string; Environment() map[string]string;
//        Close() error; Wait(context.Context) error
gate, err := m.AcquireRuntimeGate(ctx, runtimeIDs)
// gate: Seal(context.Context) error; StopIdle(context.Context) error; Release()
```

`Binding.RuntimeID`为实际runtime UUID字符串，任务副本不同于配置Agent；`Binding.AgentID`仍是配置Agent。App以PG协调器闭包绑定具体project/account/version/app/openId/brand，Source不能读取current或依CLI参数换grant。

`OpenScope(ctx)`的ctx仅约束注册准备；发布前取消会终止probe、关闭双listener且不公布scope。成功发布在锁内停止注册取消转发，之后scope由Close/Wait独立持有，不随一轮或注册ctx结束死亡。local不需要用Background临时规避该责任；其factory返回即可取消注册ctx。

`LaunchConfig`由server已校验安装manifest与本runtime目录组合，含固定native/probe/launcher路径与独立SHA、HostHome、Home、WorkingDir及恰好`InputRoots={WorkingDir}`。`LauncherBinary`必须指向固定包`bin/lark-cli`。Manager复制根列表，不读取可写描述符选择路径、环境或身份。

scope Environment只有`LOOP_NOW_LARK_EXEC_URL`和`LOOP_NOW_LARK_EXEC_KEY`。前者固定数字IPv4及`/__loop_now/lark/exec/v1`，后者为独立随机exec能力，绝非官方sidecar HMAC能力。Pi PATH第一项为scope.BinDir；那里实际安装的是独立Go shim。shim不读旧`LOOP_NOW_LARK_SCOPE_FILE`，也不裸exec native。

私有HTTP请求仅`POST`、单个Bearer exec能力及严格JSON `{"args":[...]}`；固定1MiB帧/4096参数上限，不接受binary/env/账号/root/hash/其他字段。客户端禁环境proxy与redirect。响应为有界NDJSON：`type=stdout|stderr`和base64字节data，最终`type=exit`及整数exitCode；终帧缺失/坏帧/过大帧安全失败。单帧最大32KiB、数据chunk最大8KiB、输出合计最多min(ResponseBytes,16MiB)，无真实token或provider原异常帧。

broker从服务端持有的cfg调用原`Command`，每次实际完成sandbox/参数/有界`@file`准入，之后Start独立process group并Wait；stdin为空，native环境完全由Command重建，**不继承exec URL/key**。服务端取消/客户端shim断开杀自己的原生进程组，实际Wait及组退出确认前不减consumer。子组退出不可确认时保留active，失败关闭；不伪造idle或解gate。

## Gate与sealed-ready

- OpenScope在任何安装/OS准备前，用manager.mu登记creating reservation；不存在scope的runtime同样受group gate约束。成功准备前不公布scope能力。
- exec在受理准备时登记native active，覆盖请求解析、Command/admission、Start、分页间隙、输出与实际Wait。counter不依赖业务HTTP请求数或Pi busy/idle。
- Acquire在同manager.mu下检查整组creating/native active及其他gate；任一非零整组busy，不取消在途。再冻结每个现有broker的HTTP handler admission，任何失败回滚已取得freeze；成功后同锁封住整组scope创建与命令准入。
- local正常stop与本gate.Seal可对同scope幂等Close/Wait；成功Seal证明撤销、双listener关闭、HTTP与native排空均已在事务C之前完成。Seal失败保留gate；可以重试Seal，不能以Release制造完成。
- C明确提交后Release只做幂等内存unfreeze/map操作；未知C继续持gate。尚未开始Seal时放弃获取可以Release恢复；部分stop/unknown是否可回滚由上层既有动作合同控制，不能擅自release。

## 双 loopback 与 OS 准入

同一broker独占`127.0.0.1:p`及`[::1]:p`，同HMAC/Source/handler；任一绑定失败关闭全部，任一Serve退出也触发整组Close。Wait等两Serve退出、handler及全部HTTP连接关闭。没有wildcard或单边fallback。

固定`/usr/bin/sandbox-exec` profile只允许localhost:p。新SHA独立probe协议v2证明两个自持同端口地址可达、其他自持IPv4/IPv6端口与自持UDP DNS-shaped包被OS拒绝，且子进程继承。整个probe不联系真实外部地址/系统DNS。旧probe v1不符合新协议会被拒绝，不可用旧manifest。实际OpenScope先完成同形profile准入，再公布能力；每次Command也实际检查。其他OS/架构仍unsupported。

## 新增定向验证

`TestManagedNativePATHAndGroupFence`：实际`/bin/sh`按PATH找到新Go shim→私有argv RPC→真实Command/sandbox→固定官方源码authsidecar native，向自持TLS provider完成两页。原生page-delay=1200ms期间business handler已结束，manager独立native active仍为1，exact两runtime整组freeze返回busy。进程真实完成后才取得gate；另一个尚无scope的成员创建被拒。Seal后实际拨号确认两个listener都不可达，Seal后至Release前创建仍被gate拒绝。输出无合成access/exec能力。

`TestManagedCreationReservationAndCancellation`：用SHA固定、自持阻塞probe制造真实OpenScope准备窗口，未发布scope时group freeze仍busy；取消确认自持probe父子退出、Source=0、未发布scope且创建reservation回收。

`TestManagedNativeClientCancellationDrains`：实际shim/native进入自持TLS慢响应后取消shim，真实native/upstream取消并Wait排空后才可取得gate/Seal；不是只取消假provider函数。

`TestManagedRPCRejectsWrongCapabilityAndConfiguration`：混用官方HMAC能力/exec能力、传binary/env或auth login均拒绝，Source=0；输入不能改服务端执行配置。

新增4项普通 **3.284s**（011阶段，当前manager源码）、最终012实际工件race **6.721s**，零skip。日志：`.local-build/lark-user-variant-proof/managed-native-second.log`与`managed-native-final-race.log`。首轮2项7.731s日志`managed-native-first.log`保留。`go vet ./internal/server/larksidecar ./scripts/lark-user-variant/launcher ./scripts/lark-user-variant/sandbox-probe`通过。未重原0.2全部native矩阵。

## 历史已验工件012

目录`.local-build/lark-user-variant-proof/build-012-exec-rpc/`，现生产Go源码SHA全部与manifest匹配。只从既有固定归档构建，官方源未修改；SHA均为本机平台计算，非厂商发布校验值。

| 工件 | SHA256 |
|---|---|
| 原native，保持005不变 | `7158eb571a5c1eef593001c5a959f401cd688c858a84e9dc6abc7a789a87dbd1` |
| launcher，含bin/lark-cli同字节入口 | `01df1447e07ad5c3740b8893dcf02754ab9ce768800e657a24cf19f2fabca4bb` |
| probe v2 | `52c2cbf690ea2e4ca628378141f9dbb0e5c79d0d7d8bda225129720d6ff1c986` |
| manifest.json | `bec97aa9c809f2e3986547dc8f15c07885acb341dfcd182d1715a8c213eb8752` |
| 受控tar.gz包 | `5e8f8e9086bb3fda2ca3ee8afd1269bc6eef8354b632ef0fc07aca77c6a74ec1` |

## 已验工件013：注册与发布生命周期收口

联调将注册ctx与发布后scope生命周期的分离责任从local临时包装收回manager，已由watch移除local脱钩逻辑。仅此production增量，native与probe v2字节不变，未重原native矩阵。当前目录`.local-build/lark-user-variant-proof/build-013-registration-lifetime/`，manifest全部当前production源码SHA匹配；012工件和其已绿测试保留历史，不以012旧source清单冒称当前源码。

| 工件 | SHA256 |
|---|---|
| 原native，保持005不变 | `7158eb571a5c1eef593001c5a959f401cd688c858a84e9dc6abc7a789a87dbd1` |
| launcher（entry=bin/lark-cli） | `aa6174c3ebfd8247430f3e7529b3186c1dee2a4a7983e5e69cbd2b96643e4810` |
| probe v2（不变） | `52c2cbf690ea2e4ca628378141f9dbb0e5c79d0d7d8bda225129720d6ff1c986` |
| manifest.json | `d0f99351215fc495eeda6e73d8fe8db19a2ead1cf235aac8efd84f500c6e8e9a` |
| 受控tar.gz包 | `f2051af8881f1e5dac79f97e8444b9c0c709193b7e393bd896387f0ee1c2b832` |

必要增量`TestManagedCreationReservationAndCancellation`与`TestManagedPublishedScopeSurvivesRegistrationCancellation`：真实注册准备cancel仍不发布且子进程组回收；成功发布后立即cancel注册ctx，scope真实HTTP/合成Source继续可用，最终owner Seal/Close/Wait回收。两项普通**3.407s**、race **3.635s**、零skip，日志`registration-lifetime-ordinary.log`/`registration-lifetime-race.log`。同三个包vet通过。未重012已绿实际分页矩阵。

## 当前冻结工件014：并发Seal等待也遵守ctx

watch窄审发现013的`Seal(ctx)`在进入后续ctx检查前先无条件等待sealMu，因此并发第二个Seal的短deadline可能被首个Wait拖延。属于静态可确认的有界等待缺口，不是实际App故障。最小改为cap=1的单飞channel，等待时select自己的ctx.Done；资源操作、sealed-ready与Release语义不变。

`TestConcurrentSealWaitHonorsOwnDeadline`使用真实双listener broker、自持idle TCP及在Serve前安装的最终ConnState屏障：首个Seal实际Close/Wait未排空时，第二个30ms deadline如实返回，Release仍不能解除未确认gate；放开屏障后首Seal成功才可幂等纯内存release。聚焦fixture不声称覆盖native/OpenScope准入。单项ordinary **0.826s**、race **1.846s**、零skip；日志`seal-deadline-ordinary-final.log`/`seal-deadline-race-final.log`。首轮`seal-deadline-race.log`失败为fixture在live Serve后改ConnState导致的test race，已保留并仅修测试Hook安装时机，不计该轮通过。三包vet通过；watch独立只读复核签收此缺口关闭，没有重native/App矩阵。

当前目录`.local-build/lark-user-variant-proof/build-014-seal-deadline/`；11份生产source、native/shim/probe已按manifest核对一致。native与probe v2字节不变，唯一production语义增量为上述可取消单飞等待；013及其实际App已绿结果仍保留原证据范围，不能标成014的实际App测试。

| 工件 | SHA256 |
|---|---|
| launcher（entry=bin/lark-cli） | `29009653af218b065ebe05270534d601e9428fd7071446b3a3116757a7960321` |
| manifest.json | `941039beb0ec81ac39f90373400d83f5722684460f3455400961bb5df466fcc1` |
| 受控tar.gz包 | `84e0846a40dcb38a8f091c3402ccd657faf3ff60a54649b243c5854dddeb60a9` |

native SHA仍为005的`7158eb571a5c1eef593001c5a959f401cd688c858a84e9dc6abc7a789a87dbd1`，probe仍为`52c2cbf690ea2e4ca628378141f9dbb0e5c79d0d7d8bda225129720d6ff1c986`。server唯一负责同步资产pin及必要安装切片，不因此重原业务矩阵或执行部署。

## App/local真实源码衔接与证据限制

已只读核到server实际`app/lark_runtime.go`持有同一个manager并分别提供local factory和tasks.TaskLarkNativeManager adapter；`app/app.go`同时装配二者，非两套注册表。`app/lark_assets.go`已使用012准确manifest pin及同名原生/shim/probe路径。watch独占local已经消费LarkFactory，传实际RuntimeID与Home/Workspace，PATH加入shim、跳旧bot渲染/安装，并将资源Close/Wait纳入进程释放。没有并写这些文件。

随后server完成了独立实际App验证（证据由其唯一维护）：`TestLarkNativeBundleActualAppAdmission`普通4.869s；`TestLarkNativeAppCapturedAThenNextRoundB`为TEST App/PG→local/fakePi→PATH shim→本manager→固定native两页→PG TokenSource，当前轮固定A、下轮B，普通10.216s；两项必要race日志`native-app-install-capture-race.log`为20.228s通过，server已确认实际运行。详见change的`server-verification.md`「受控发行安装与实际App/native接缝」。这不是本owner另跑或重做原矩阵。首轮fixture未透传`--version`的失败由server修测试入口并保留日志，不归因为生产故障。

server随后采用当前013并完成必要影响复验：`native-app-final013-race.log`，2top/0skip，安装+probe/App admission 7.59s；normal A/B以及真实TEST task Create→账号新current→重启/Retry保旧grant 12.68s，包race **22.341s**全过。见server-verification「当前工件013与安装任务3.2收口」；其记录工作目录、监听、TEST schema/Redis均自持清理。此前跨owner生命周期修改窗口中task页未到的首失败另保留，不能作为通过。本owner只读核对这些owner证据，未重复执行App测试。

server最终014当前工件证据已补：`native-app-final014-race.log` **3top/0skip/35.654s**，实际两native consumer共用真实PG bound TokenSource、两次合成到期各唯一轮换12.29s；完整bundle安装/probe/App同manager装配6.27s；normal A/B、真实TEST task Create后current换版本、restart/Retry、blocked普通Inbox聊天与sleep原deadline重启/提前醒保A且不推进workflow13.45s。Source接缝实际为`app/lark_runtime.go`直接`grants.TokenSource(request.Binding)`再交`Manager.OpenScope`，没有另造假Source。该组合与cleanup由server唯一执行和记录，本owner只读核对，不重新运行。

这已超过纯组件证明，但仍不替代完整任务switch/sleep全部矩阵、长轮次多consumer/两次PG refresh矩阵或真实OAuth/QA。没有对当前服务/真实task/用户凭证/真实OAuth/QA/模型/Git进行操作。所有本owner新增测试监听、子进程、client和临时目录按fixture取消/Close/Wait回收；App TEST资源清理由server证据负责，不把本组件cleanup替代App清理回执。发现具体ABI缺口继续由本owner修包。
