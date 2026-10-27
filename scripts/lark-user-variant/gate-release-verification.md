# 原Manager ABI与gate安全回滚收口

2026-10-20，Main已确认保原Manager ABI与Release状态区分；不新增Abort、不迁移facade、不改App/local/tasks/pins，不运行现有服务、真实OAuth/QA/provider或现库。

## 最终口径

- `NewManager/OpenScope → *ManagedScope`；scope的`ExecURL/ExecCapability/Close() error/Wait(ctx) error`不改名。Close返回Broker关闭错误，不能把Close本身当已排空。
- `AcquireRuntimeGate → *RuntimeGate`只gate；创建reservation和native从准备到实际Wait结束仍走同一registry。
- `StopIdle(ctx)`调用真实Seal、关闭及Wait；成功后重复调用成功。`Release()`只做幂等内存操作。
- **未开始Seal的Acquire回滚允许Release；已开始Seal但未确认成功必须保gate。** 前者不stop、不改旧scope，不把“允许回滚”叫sealed-ready；后者不因Release丢失未知清理。上层在C未知时仍须保自己持有的sealed gate，不提前调用Release。

## 已撤回候选及真实冲突

本线程一度按“未sealed拒释放”的字面收紧为`!g.sealed`。CLI/watch核实已有生产路径：`tasks/lark_switch_operation.go`先native Acquire，后local Acquire判busy/starting返回nil lease，经abort动作转到releaseLarkSwitch。该阶段尚未stop/Seal，必须纯内存撤自己的native gate；一律拒Release会在服务层删除held记录后遗留gate，强行Seal又会停止忙Pi的scope。

Main已明确保原区分、撤回该候选条件，无需新Abort接口。候选的5top ordinary2.016/race1.848虽绿，**只证明局部条件，不能算业务通过**。候选日志、hash及5源快照保留在`.local-build/lark-user-variant-proof/gate-unsealed-release-final/`，源码快照在`rejected-sources/`；没有把这些绿结果覆盖为最终口径证据。

## 最终必要定向回归

```sh
go test ./internal/server/larksidecar -run '^(TestRuntimeGatePreSealRollbackAndRepeatedStopIdle|TestConcurrentSealWaitHonorsOwnDeadline|TestNativeManagerCloseJoinsBeforeRegistryRelease)$' -count=1 -v
go test -race ./internal/server/larksidecar -run '^(TestRuntimeGatePreSealRollbackAndRepeatedStopIdle|TestConcurrentSealWaitHonorsOwnDeadline|TestNativeManagerCloseJoinsBeforeRegistryRelease)$' -count=1 -v
go vet ./internal/server/larksidecar
```

ordinary **0.835s**、race **1.868s**，3项、零skip，vet exit0。

第一项直接用原Manager/ManagedScope/RuntimeGate ABI，真实自持双listener：pre-Seal重复Release恢复命令准入、不关闭scope；重新Acquire阻新命令；真实StopIdle/Wait成功且重复幂等，最后重复Release才移除gate。第二项使用既有真实双listener/连接结束屏障，Seal未确认期间Release拒释放、并发短deadline生效。第三项复用先前未发布facade fixture验证底层Close/Wait失败保精确registry，只是底层fixture回归，不代表迁移或App/native集成。

所有TokenSource仅返回合成错误；provider调用0，监听/连接/目录均自持并cleanup/Wait回收，无App或真实native矩阵重跑。最终日志及源码hash：`.local-build/lark-user-variant-proof/gate-safe-rollback-final/{ordinary.log,race.log,vet.log,source-sha256.json}`。旧014结果保持原范围，未生成替代工件。
