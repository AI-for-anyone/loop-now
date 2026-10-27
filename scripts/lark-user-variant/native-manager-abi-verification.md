# Native manager facade 未发布差异记录

**已撤销迁移，不是当前运行合同。** Main ownership revision12明确保留原Manager/ManagedScope Close()+Wait及现App/local/task薄适配；不迁移NativeManager，不改014/pins。本文件及其源码/fixture只保留当时已授权切片的未发布证据，不作为新产品入口或App接线完成。后续Release歧义已经Main裁定保原“pre-Seal可纯内存回滚；attempted未sealed保gate”，见 [gate-release-verification.md](gate-release-verification.md)。以下是当时切片记录，不能覆盖该最终口径。

2026-10-20。本轮只修改plugin独占的larksidecar及本目录记录；不改App/local/tasks、OpenSpec、现库、运行服务、系统配置或Git，不请求真实OAuth/QA/provider。旧014工件及其首失败/验证日志保持原样。

## 实际可编译入口

```go
m := larksidecar.NewNativeManager() // App唯一实例，同时用于factory与task交接
scope, err := m.OpenScope(ctx, binding, source, cfg, limits)
// scope: *NativeScope
scope.BinDir() // string，固定受控PATH shim所在目录
scope.Environment() // 仅LOOP_NOW_LARK_EXEC_URL / LOOP_NOW_LARK_EXEC_KEY的新map
err = scope.Close(ctx) // revoke + broker/native drain，成功后才释放精确registry
lease, err := m.Acquire(ctx, runtimeIDs) // exact2、非零去重、复制后排序
err = lease.Seal(ctx) // 所有可失败关闭/排空，保留gate
lease.Release() // 无返回、纯内存、幂等
```

`Binding.RuntimeID`仍为真实runtime UUID字符串。配置/binding/token source由App可信捕获；NativeScope不导出原签名key、descriptor或LaunchConfig。没有tasks/local包依赖。server持薄适配，watch持local factory；此记录不声称App已切至新ABI。

`NativeManager`只封装既有一个Manager引擎；兼容旧Manager/OpenScope/Close+Wait签名以免并写或破坏现App编译，新增入口没有另造scope表、active计数或group gate。所有新OpenScope/Acquire/Seal均实际进入同一引擎。App不得各自创建执行manager和task manager。

## 边界与实现

- OpenScope在固定工件检查、双listener/OS admission之前，已在core同锁登记creating；exec由受理准备开始计active，贯穿Command/Start/Wait及输出排空。底层真实native路径沿既有实现，不由此轮fixture冒充新证明。
- Acquire只gate，既不stop也不取消现scope；exact2排序不修改调用方slice。无scope也可冻结；有creating/native active/unknown completion或HTTP busy整组拒绝。CLI按既有顺序先Acquire、再local.StopIdle、再Seal。
- NativeScope.Close(ctx)先撤销/关闭，再确认broker双listener、handler/connection及native consumer已排空。超时保精确registry，不能重注册；取消等待不撤销已执行的revoke。Close可用新有界ctx重试；完成后幂等。额外修正底层Wait：在同锁删除registry前检查ctx，一旦删除则返回已确认成功，不出现“已删登记但返回取消”。
- Seal在gate内关闭/Wait所有捕获scope，成功仍保gate。失败或未知不能Release制造完成。C确认后Release仅幂等内存unfreeze/map删除；在开始Seal之前放弃Acquire可原路内存回滚。没有HTTP/socket/Wait/DB操作藏在Release。

## 必要定向验证

命令：

```sh
go test ./internal/server/larksidecar -run '^(TestNativeManager.*|TestConcurrentSealWaitHonorsOwnDeadline)$' -count=1 -v
go test -race ./internal/server/larksidecar -run '^(TestNativeManager.*|TestConcurrentSealWaitHonorsOwnDeadline)$' -count=1 -v
go vet ./internal/server/larksidecar
```

ordinary **1.959s**；race **3.014s**；两者4项、零skip，vet exit0。

| 测试 | 实际证明 |
|---|---|
| TestNativeManagerAcquireExactPair | 空/单/三项/零/重复拒绝；exact2独立排序；无scope创建同gate拒绝；准备reservation和未确认consumer均busy |
| TestNativeManagerCloseJoinsBeforeRegistryRelease | 真实自持双listener；受控准备计数未释放时Close超时且精确registry保留、不能重注册/Acquire；完成屏障后Close成功且幂等；仅两exec env、不回传原key、source0 |
| TestNativeManagerAcquireOnlyGatesSealCloses | 真实自持双listener；Acquire不close但阻新命令，Seal完成实际broker drain且不release gate；Release后才可再Acquire |
| TestConcurrentSealWaitHonorsOwnDeadline | 改用新NativeManager/NativeLease；真实双listener与HTTP连接退出屏障；第二Seal独立deadline，首次未确认时Release不解gate；实际排空后才sealed-ready |

fixture全部为自持loopback监听及合成值；TokenSource只返回固定错误，不发供应商请求。前三项中的准备/未确认consumer状态由测试屏障控制，不声称新增真实native/PG证据。此前014真实PATH/native/PG矩阵不重跑也不覆盖旧结果。所有监听/连接通过cleanup及Wait回收，未启动App或触碰现有服务。

日志及源码冻结清单：`.local-build/lark-user-variant-proof/native-manager-abi-final/{race.log,vet.log,source-sha256.json}`。本轮不构建新native/shim/probe包，不刷新manifest或安装pin；014不能冒称包含新facade/Wait修改。由Main/server统一后续候选装配与验收。
