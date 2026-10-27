# 生产 sandbox 准入收敛：当前未通过（2026-10-20）

这是 Main 新授权后的独立门禁记录，不覆盖已经通过的 native0.2 历史矩阵。源码单写范围仍为 larksidecar 与本目录；server 持PG／执行器／Store，watch只读审查。未触现有服务、系统设置、Keychain、真实供应商、用户凭证、业务task或Git。

## 确切失败，不标 supported

1. 旧生产profile使用 `(remote tcp "localhost:<p>")`。在 broker 的 IPv4 端口旁自持 `::1:<p>` listener 后，无沙盒正控制可达，旧profile中的实际子进程也可达。`ipv6-policy-first.log`保留失败；不是无监听端口／timeout误判。
2. 直接使用 `(remote tcp "127.0.0.1:<p>")`，本机sandbox编译器拒绝：`host must be * or localhost in network address`。`numeric-policy-second.log`保留native前置失败，不称修复成功；该轮generic失败文案不能当另一次IPv6允许证明，准确原因是策略拒绝。
3. 本机系统profile提供了`socket-domain AF_INET6`语法依据，但 `localhost:<p> + deny system-socket AF_INET6` 仍未阻止同端口IPv6连接。固定平台probe的 raw Socket/Connect 结果见 `admission-diagnostic.log`：`allowed=true other4=true same6=false other6=true dns=true inherited=false`。其中other/same/DNS布尔代表“明确拒绝”，所以same6=false就是未通过；编译器受理并不证明内核约束生效。该结果未使用真实外部地址或外部DNS。

**当前不能将 darwin-arm64 标 supported。** 新生产Command在准入失败时返回nil,error，不返回可启动native Cmd，不读TokenSource、不回退旧variant／bot／host／direct。既有业务native矩阵仍为真实历史证据，但不替代这个新增生产网络门禁。

## 生产准入实际代码

- `launcher.go`：固定numeric IPv4 URL、1..65535 canonical端口；仅darwin-arm64，固定系统sandbox入口；`LaunchConfig`新增具名 `ProbeBinary/ProbeSHA256`，TokenSource／Start／Binding／Command函数签名不变。probe必须是平台可信工件，路径／执行位／摘要准入，不从HOME/PATH找探针。
- `admission.go`：构造即将交CLI的同一profile；用相同基础干净环境（不传capability／token／appSecret）、自持正控制，验证broker端口、其他IPv4端口、IPv6同端口／其他端口、自持UDP DNS形态。只将EPERM／EACCES算明确拒绝；refused／timeout／crash／策略编译拒绝均失败。结果绑定随机nonce、profile／broker地址／native SHA／probe SHA，不缓存全局布尔；派发前再检查broker未Close。无ExtraFiles／已联网fd传入。
- `scripts/lark-user-variant/sandbox-probe/main.go`：平台自己的独立stdlib-only binary，随bundle附SHA与源文件SHA，不是官方CLI修改，不是Go test自重启。探针子进程继承同profile，不带认证能力。当前`inherited=false`未通过，不能宣称子孙网络限制验收成功。
- 上下游frame均有界；4秒独立总deadline、独立probe进程组、取消Kill组及WaitDelay、等待退出与自持listener回收。普通错误仅固定阶段／布尔／exitcode，不输出原stderr／环境／请求体／秘密。
- `build.py`会打包`lark-cli-sandbox-probe`与manifest。007／008／009均为本轮**诊断工件**，不是生产可用候选；原官方source与CLI binary不变。server在新的准入通过前不得据bundle存在宣称已支持或绕过Command。

DNS字段目前严格是“自持numeric UDP端点上的DNS形态包”；没有访问外部解析器。Darwin Go的系统resolver/IPC不能由该负例概括为全DNS已阻断。native基础环境固定`GODEBUG=netdns=go`避免依赖默认Darwin resolver选择，但它不是OS级全DNS证明，更不是硬内存限制。host直连用自持原目标／其他端口验证，未探测真实provider或宿主现有服务。其他127地址同端口未通过别名配置凑fixture，也未修改系统网络。

## 已通过的失败关闭／资源收尾检查

- `admission-failclosed-race.log`：缺sandbox、缺probe、摘要错、实际IPv6门禁失败、SBPL拒策略，全部不返回native Cmd、Source0；**PASS 1.683s**。缺sandbox使用包私有依赖注入路径；正式入口始终固定`/usr/bin/sandbox-exec`，没有生产测试开关。
- `admission-cancel-second-race.log`：测试专用假probe只创建自持sleep子进程，不入生产bundle；取消真实生产admission后整个自持probe组与子进程均不存在，Wait排空，Source0；**PASS 3.626s**。首轮Sscanf多返回值编译缺口保留在`admission-cancel-first-race.log`，修后未改生产协议。
- `go vet ./internal/server/larksidecar ./scripts/lark-user-variant/sandbox-probe`退出0。

这些绿项证明“失败时不派native”和资源回收，不证明成功准入；没有因此重跑旧native业务矩阵或把失败的精确地址门禁改成通过。

## 内存门禁：有限输入与硬限制分开

本轮没有设置GOMEMLIMIT，也没有声称broker字节cap解决CLI预读。原官方interceptor仍先ReadAll。之前真实32KiB快照／源文件增长至2MiB／约38.4MB RSS仅为合成fixture观测，保留历史不重跑，不是硬RSS上限。

新增确切缺口：原`boundedInputs`每个@JSON各自受限，但等所有文件展开才总计argv，可先累计N倍预算；`input-aggregate-first.log`真实复现。最小修复是先验原argv总量，再按剩余总预算逐文件LimitReader，快照合计在预算内即停止，不等全部展开。普通JSON快照、FIFO、聚合预算定向race：`input-budget-final-race.log` **PASS 3.134s**。第一次修复回归暴露旧单位测试原来只给文件大小预算而未包含整条argv，失败留`input-aggregate-second-race.log`，正常输入fixture改为包含整条argv的预算；未放宽生产总限额。

受控原生QA纯文本/API JSON的输入来源边界：@data／@params在native前有界快照，argv总字节受限，无无限stdin／multipart／显式媒体文件入口；普通im发送／回复的text/content声明没有File/Stdin来源（官方shortcuts/im/im_messages_send.go:28–34、reply.go:26–32与common/types.go:28），不会把这些inline文本自动当文件读。Markdown另有图片URL下载路径，不把它当纯文本零额外IO；当前网络准入未过，不能外推该路径已安全支持。整个im命令树／任意jq／任意未来shortcut的硬内存控制没有证明，本轮不扩CLI范围、不patch官方源。

最小后续若要求“所有CLI路径硬RSS限额”而不是受控QA文本输入：需另行明确可验证的进程资源执行策略；GOMEMLIMIT或单次RSS观测都不能代替。本轮没有擅自新增此政策。

## 待Main裁定的最小网络修正

已将真实OS限制和最小同路线建议交Main：让同scope broker同时持有127.0.0.1与::1该端口，接管OS允许的双loopback地址，保持官方tag／HMAC／真实token仅平台；这改变原单IPv4 listener，**尚未授权实施，尚未验收**。如果继续要求OS精确只允许IPv4地址，本轮SBPL路径当前unsupported／failclosed，不能假称已表达该策略。未启用CONNECT／私有CA、未切Provider或任何替代认证栈。


## 权威workspace映射与额外输入修复（后续增量）

Main/server经watch明确生产值：`Home=d.home`、`WorkingDir=d.workspace`、`InputRoots=[]string{d.workspace}`，取实际执行runtime UUID的目录；不按配置AgentID或用户CWD/PWD/argv猜根。实现已替换本记录上文早期“默认HOME”临时方案：生产无默认输入根，字段缺失／根不等WorkingDir／HOME和workspace相同或互相嵌套均先拒绝；相对@file以WorkingDir为基准。HOME只供私有配置，不作业务JSON来源。

watch发现server预读在sandbox前接受外部绝对JSON或父symlink，可绕后置host文件deny。用完全自持的外部临时JSON复现，`input-scope-first.log`首失败保留；未读取真实host文件。新增input.go从目录fd逐级Openat NOFOLLOW，最后只开非阻塞普通文件；路径须落Go可信InputRoots，不能用argv添根。父symlink／外部根／私有HOME不得作为业务输入；根和文件句柄成功失败均关闭。这里不是全面宿主文件系统沙箱，不扩CLI权限。

必要定向结果：`input-scope-second-race.log` **PASS 2.960s**；后续显式workspace正例、元数据缺失／错误／嵌套6负例与准入/组回收合并结果见 `workspace-admission-final-race.log`。本次仍没有重跑已通过的真实分页／OAuth业务矩阵，没有改变官方source／固定CLI binary／TokenSource或Start签名。

LaunchConfig新增具名字段WorkingDir／InputRoots／ProbeBinary／ProbeSHA256；server/执行器唯一owner提供可信实际目录和校验工件。先前build-006缺probe不能启动新的生产Command；007–009是诊断历史，不能按其目录存在即标supported。最新源码四类门禁（网络失败关闭、probe组回收、输入根及聚合预算）均有独立证据；网络精确地址失败仍不作通过。

最后输入路径收尾：`TestInputDotDotCannotEscapeWorkspace`验证相对逃逸；`TestInputDirectorySwapCannotRedirectPinnedFD`在生产rootfd验证后用channel屏障rename父目录、替换成指向外部自持JSON的link，再调用生产relative open，必须拒绝；恢复后同rootfd正控制可读inside。没有生产fixture开关。目录fd复制用Openat(".", O_CLOEXEC)原子设置close-on-exec。`input-barrier-final2-race.log` **PASS 1.823s**，只跑输入相关格；`workspace-admission-final-race.log` **PASS 3.703s**。source回执在`sandbox-admission-sources.json`，`sandbox-admission-result.json`明确supported=false；009诊断包不表示最新生产源码候选。新native负例未继续派发，因为本轮生产准入正门禁仍失败；没有绕过该失败来制造绿native结果。
