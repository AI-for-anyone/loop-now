# 原Manager的pre-C native恢复最小接缝（待内部边界确认）

2026-10-20。本文件是用户要求先交的源码依赖分析与最小方案，不是已实现或已生效恢复契约。当前尚未新增恢复生产源码、测试、candidate或pin；不采用facade/Provider/descriptor，不改变014工具工件。

## 已核事实与缺口

- 原`Manager.AcquireRuntimeGate`在同锁下要求creating/native active为零，且各scope的HTTP handler可FreezeIdle。任一native还在准备、Start、Wait、输出或组退出不确定中都拒绝。
- `execute`的consumer从受理准备起登记，只有未启动子进程，或实际Wait后`confirmProcessGroupExit`确认组不存在，才执行end。因此合法原gate捕获点不可能还有可合法记录为存活的native组，不能为展示kill测试绕过此边界。
- 目前没有scope随机epoch、原owner出生身份、持久的原gate零组/已确认组水位，也没有native typed Capture/Recover。重启的新registry空、端口不可达、Pi死亡均不能补这些证明。
- CLI已持内部`recovery_record {local,native}`与只读pending action清单，local已实现其独立typed记录；当前native仍缺，不能据local receipt解fence。记录在A后、首次StopIdle前提交；此前崩溃缺记录就是unknown。

## 拟保留的原API与最小新增方法

只扩原Manager/RuntimeGate，不改已有公开兼容签名、不引入第二registry：

```go
// 名称/参数由server/plugin一次锁定后实施，目前仅提案。
func (g *RuntimeGate) CaptureRecovery(ctx context.Context, actionID uuid.UUID) (NativeRecoveryRecord, error)
func (m *Manager) RecoverPendingHandoff(ctx context.Context, pending PendingNativeHandoff, record NativeRecoveryRecord, verifyPreCommit func(context.Context) error) (*RuntimeGate, NativeRecoveryReceipt, error)
```

`verifyPreCommit`由server/CLI的可信Go闭包捕获原task/actor/action及exact两runtime，锁内查询真实原action和fence，不从HTTP/CLI输入接收一个可自填的“已确认”布尔值。恢复先持原Manager的两runtime creation/admission gate；调用PG回调不持manager.mu；返回成功receipt前再复核pre-C边界。最终PG fail/C outcome处理仍由原owner做，Manager不访问Store。

记录来自原held gate，版本化包含原action、原manager epoch、hostname/boot/原owner kernel PID+UID+birth；exact两runtime成员各记明确无scope或原scope epoch及已确认零组水位。可加非秘密的原binding版本摘要和已确认native Start/Wait计数以防混代；不记录代理key、exec cap、token、env或命令正文。

所有字段只由原Manager真实capture，再由原action事务原样持久化。新空registry不得制造原成员Absent；scope epoch不是PID，也不能从runtime UUID重新推导。记录的具体字段还需与server锁一次typed ABI。

## 必须限定pre-C，不能偷换为C后恢复

watch已明确认可的最小交集：只有原action锁内仍为pending/handoff、exact两原PG fence完整、C未提交/未Release，并保持startup dispatch暂停，才消费原idle水位。原gate之后禁止Prepare/Start；只有在此边界成立时，原捕获的零组事实才没有被后续native启动作废。

C成功、C未知、缺任一PG事实，或只看到publisher continuation queued而没有另一成员的准入证明，都必须unknown/保暂停。C后Release/kick可能产生新native；旧水位不证明它们不存在。覆盖C后需要另有逐launch可信持久账本或两成员完整准入证明，本轮不自行扩展该恢复范围。

## 内核证明与保守失败

- 复用local已验证的darwin `kern.proc.pid` PID/UID/birth与`kern.bootsessionuuid`读取语义，放sidecar自身平台私有文件，不import local或tasks。
- 原owner同birth仍存活：unknown，不杀整个server。身份读失败、PID复用/出生不符、host/boot不符：unknown，不向不明PID/PGID发signal。
- 原owner必须由内核确认已死亡，才能结合broker listener只由其持有、FD未继承给native且未导出socket句柄的事实证明旧cap不可再服务；不能只测HTTP dead。
- 只有原合法gate已零组、水位有效且旧owner死亡都成立，才可返回native恢复receipt；receipt之后仍保gate供原action锁内确认。缺记录、组不确定、陈旧水位或任一校验失败返回held gate+unknown，不能调用Seal把空scope强行变ready，也不能Release失去屏障。
- 本合法capture点没有可停止的live native组。任何确有live孤儿但缺原可信身份账本的分支只能unknown；不提供猜最新PID、按名称搜索、盲杀组或删除workspace的回退。正常执行中原有取消/Close/Wait停止行为不被此提案扩大。

## 实施前仍须一次锁定

1. server/CLI提供可信PG pre-C复核闭包的准确捕获/锁图与调用顺序，确保不把A后/C已提交未知结果误判为pre-C。
2. Main确认本轮仅补上述合法idle-capture/pre-C范围；不能把它写成C后任意native孤儿停止已验。
3. 与local receipt组合时任一unknown保原fence/dispatch暂停。跨进程正例应真实退出旧owner再核内核死亡；同一测试进程仅新建App/Manager不能冒原server死亡。

锁定后仅实施本切片及确定性自持进程/记录负例，不重App/native旧矩阵、不访问现有服务/库、宿主凭证或真实OAuth/QA/provider。
