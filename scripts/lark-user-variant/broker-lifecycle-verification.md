# Broker冻结/退出资源接缝

2026-10-20，独占`internal/server/larksidecar/`增量。没有改执行器、Store、API、任务数据、现服务或正式规格，没有运行真实供应商/原native矩阵。

## 稳定接口

```go
func (b *Broker) FreezeIdle() (release func(), err error)
func (b *Broker) Close() error
func (b *Broker) Wait(ctx context.Context) error
```

`FreezeIdle`与handler准入、slot占用及active递增共用`b.mu`。active非零或已经冻结返回`ErrBrokerBusy`且release=nil；已关闭返回`ErrBrokerClosed`。它不取消请求、不等待在途请求自行结束来制造idle。成功后新请求固定503，不解析秘密或调用Source。release幂等，仅解除自己持有的冻结；旧release不会解除后续新冻结，Close后release也不复活。

`Close`保持原签名，新增once幂等；标closed/frozen、取消上下文并关闭HTTP服务/idle上游连接。`Wait`必须在Close后调用，否则返回`ErrBrokerNotClosed`。它依次确认Serve退出、已准入handler返回以及HTTP连接goroutine最终Closed（包括冻结后拒绝请求的响应收尾）。超时返回原ctx错误，绝不将listener已关闭视为handler已排空。取消不合作的Source无法被Go强杀，这时Wait如实超时，上层不得继续宣称停止成功。

**仅为broker handler层资源证明**：native CLI可能在两次分页之间没有HTTP请求，仍是活跃consumer。独立exec manager须另外从接受准备计数到Start/Wait确认，并与runtime gate共锁；不能用本FreezeIdle替代native consumer gate。上层双runtime先全组freeze成功再stop；冻结任一失败时release已取得部分。Close/Wait及能力撤销等可失败工作应在既定事务C前完成，提交后只做幂等release；本包不实施数据库/恢复事务。

## 定向证据

- `TestBrokerFreezeIdleAdmissionAndRelease`：冻结拒新请求且Source=0；release恢复；重复release不解新lease；Close后不复活；Wait在Close前拒绝、Close后确认drain。
- `TestBrokerBusyDoesNotCancelAndWaitProvesHandlerExit`：真实自持loopback HTTP进入合成Source并在屏障阻塞；Freeze返回busy且请求ctx未被取消；Close两次成功后短deadline的Wait仍超时；只在Source真实返回、客户端回收及handler/连接active均为0后Wait成功。合成Source不返回token，不会访问真实upstream。
- `TestBrokerFreezeAtomicWithHandlerAdmission`：100轮同锁真实handler准入/Freeze竞态；freeze胜出时Source不得进入，handler胜出时必须busy；每轮Close/Wait排空。另有上一项确定性busy屏障，不依赖随机调度覆盖busy分支。

执行：`go test [ -race ] ./internal/server/larksidecar -run '^TestBroker(Freeze|Busy)' -count=1 -v`。

普通PASS **0.835s**，race PASS **1.876s**，均零skip；日志分别`.local-build/lark-user-variant-proof/broker-lifecycle-ordinary.log`和`broker-lifecycle-race.log`。`go vet ./internal/server/larksidecar ./scripts/lark-user-variant/launcher`通过；另对原签名/重放单项普通检查通过3.806s，没有重复原native矩阵。

测试自持监听、客户端与goroutine均Close/Wait，临时目录由测试回收。未接当前App，不宣称双listener/exec manager/Pi/switch或真实认证验收通过。010旧包的源码manifest不覆盖本增量，未重新打包为可安装supported工件。
