# 当前Wait登记保护与最小join/gate收口

2026-10-20，按Main本轮直接收口指令保留dirty兼容facade，停止ABI改名/迁移，不要求App/local重接，不组candidate，不重native/App矩阵。本轮Go源码零修改，仅运行下面三项最小fixture并记录当前摘要，整体冻结由Main统一。

## 精确生产差异

当前`manager.go` SHA256：`3114a79f493102a191e650083112182a5db582f873c2f9245a1e2f5fb72e3480`。

相对旧014的`manager.go`记录`ce2901bd8cea56b9b6d1b76b38985e7866976e215f862143409fa1fe9598f6b2`，已交源码增量为：

1. `ManagerOptions.HTTPTransport`、`NewManagerWithOptions`与OpenScope构造期依赖传递（App隔离门禁已明确要求的DI）；`NewManager()`仍nil生产transport，非运行期setter/env/API。
2. `ManagedScope.Wait`取消返回与精确registry删除的最小修正：

```diff
 m.mu.Lock()
+if e := ctx.Err(); e != nil {
+    m.mu.Unlock()
+    return e
+}
 if m.scopes[s.id] == s {
     delete(m.scopes, s.id)
 }
 m.mu.Unlock()
-return ctx.Err()
+return nil
```

broker/handler/连接排空及consumer idle确认仍先于这段代码。取消导致错误时不删除登记；删除是确认完成后的同锁操作，完成后返回成功。旧scope只可删除同一精确指针，不能删除后来同runtime的新scope。

`native_manager.go`只是已落的兼容wrapper，保留同一core，不新增registry；当前App/local/tasks不引用该生产入口。不要求迁移。测试依赖仍存在，禁止孤立删文件造成编译失败。`gate_seal_test.go`已使用原Manager/AcquireRuntimeGate。Release仍保Main最终口径：未开始Seal允许纯内存回滚；已开始但未成功保gate；sealed后的Release纯内存幂等。本轮未再反改任何一处。

## 当前源码的必要结果

```sh
go test ./internal/server/larksidecar -run '^(TestNativeManagerCloseJoinsBeforeRegistryRelease|TestRuntimeGatePreSealRollbackAndRepeatedStopIdle|TestConcurrentSealWaitHonorsOwnDeadline)$' -count=1 -v
go test -race ./internal/server/larksidecar -run '^(TestNativeManagerCloseJoinsBeforeRegistryRelease|TestRuntimeGatePreSealRollbackAndRepeatedStopIdle|TestConcurrentSealWaitHonorsOwnDeadline)$' -count=1 -v
```

ordinary **1.971s**、race **1.850s**，各3项、零skip。测试前后校验5个相关Go文件SHA完全一致，故结果对应下列当前稳定输入，不冒称旧014 manifest zero drift。

- join fixture：真实自持双listener与受控准备计数屏障；Close/Wait超时保精确registry、拒重注册与Acquire，完成屏障后幂等Close才成功；Source0。wrapper只作为现有fixture入口，不是App集成证明。
- 原ABI gate fixture：pre-Seal重复Release恢复准入但不stop scope；重新Acquire阻命令，StopIdle实际关闭与Wait排空，重复调用和sealed后Release幂等。
- 并发Seal fixture：真实双listener/最终连接关闭屏障；首Seal仍在drain时第二30ms deadline及时返回，Release仍不能释放未知gate；放开屏障后首Seal正常完成。不是native进程或重启死亡证明。

日志及完整5源SHA：`.local-build/lark-user-variant-proof/wait-join-gate-current-final/{ordinary.log,race.log,source-sha256.json}`。无真实provider/OAuth/QA、App、PG、现有服务或Git操作；所有监听/连接和测试屏障有cleanup/Wait。旧014工件、manifest、pins及其历史测试严格保留原输入归因，本轮不替换或重新构建它们。

watch已独立只读回签：当前Wait先broker drain/consumer idle后锁内ctx复核，错误不删登记，仅同scope指针可删且之后返回nil；Release的pre-Seal回滚/attempted未sealed保gate/sealed幂等内存释放均通过。5源SHA全部匹配，ordinary1.971/race1.850各3项零skip日志已读；watch未重测，不将wrapper fixture外推迁移、App/native或重启死亡证明。该窄审已收口，后续整体冻结仍归Main。
