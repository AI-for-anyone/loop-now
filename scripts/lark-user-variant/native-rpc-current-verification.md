# 当前Manager真实RPC链定向复核

2026-10-20，按当前直接派工仅运行既有`TestManagedNativePATHAndGroupFence`的ordinary/race各一次。未修改Go、shim、probe、native、manifest或pins；未构建新包、未启停现服务、未访问现库、真实OAuth/QA/provider/模型或Git。

## 固定实物与边界

使用既有`.local-build/lark-user-variant-proof/build-014-seal-deadline/`：

| 项目 | SHA256 |
|---|---|
| manifest | 941039beb0ec81ac39f90373400d83f5722684460f3455400961bb5df466fcc1 |
| native | 7158eb571a5c1eef593001c5a959f401cd688c858a84e9dc6abc7a789a87dbd1 |
| probe v2 | 52c2cbf690ea2e4ca628378141f9dbb0e5c79d0d7d8bda225129720d6ff1c986 |
| RPC shim及bin/lark-cli副本 | 29009653af218b065ebe05270534d601e9428fd7071446b3a3116757a7960321 |

已实际读文件重算摘要一致；launcher.protocol=`loop-now-lark-exec-v1`，未引用010。官方固定commit/archive/tag保持014原记录，未重标为官方Release。生产source是当前工作区Manager实现；不声称其全部源码清单等于旧014 manifest，未发布facade/Wait修改保持先前记录范围。

本轮用实际`/bin/sh`测试PATH入口，不启动真实Pi或App。provider为自持TLS fixture，package-private transport固定将预期host映射到该fixture，其他host拒绝，无真实供应商请求；TokenSource只返回合成access值，不是PG source。PG组合证据仍归server原记录，不由本测试替代。

## 命令与结果

```sh
LARK_USER_VARIANT_BINARY="$PWD/.local-build/lark-user-variant-proof/build-014-seal-deadline/lark-cli" GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go test ./internal/server/larksidecar -run '^TestManagedNativePATHAndGroupFence$' -count=1 -v
LARK_USER_VARIANT_BINARY="$PWD/.local-build/lark-user-variant-proof/build-014-seal-deadline/lark-cli" GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go test -race ./internal/server/larksidecar -run '^TestManagedNativePATHAndGroupFence$' -count=1 -v
```

ordinary **4.990s**（test body4.16s），race **3.217s**（test body1.38s），各1项、零skip。未重旧native矩阵、未重新扩大输入/内存/响应/PG测试。

## 实际证明

1. OpenScope和实际Command均经过现有生产exact-profile admission。固定probe对两个本broker地址`127.0.0.1:p`/`::1:p`验证可达，对其他自持IPv4/IPv6端口、UDP DNS-shaped fixture验证EPERM/EACCES，包含子进程继承；拒绝用refused/timeout冒deny。无真实外部探测或系统DNS。127别名/外部地址仍为既有未验证边界，不增加probe-v3。
2. 实际shell PATH找到固定Go shim，只传独立exec URL/key与有界argv到私有RPC；server-held cfg进入Command/sandbox，实际Start/Wait固定native，完成两页合成消息读取，未用descriptor authority或裸exec。
3. 两页间1200ms延迟中，business请求已结束，manager独立native active仍为1；exact两runtime整组Acquire返回busy。实际Wait及输出结束后才可取得gate，不用HTTP idle或Pi idle替代consumer。
4. 无现存scope的另一个runtime同样禁止OpenScope；Seal实际撤销并排空，之后两listener自持地址均已不可达；Seal后到Release前creation gate保留，Release重复调用幂等纯内存。
5. 输出检查不含合成access或exec key。监听、probe/native进程组、TLS fixture及临时HOME/workspace由既有测试defer/cleanup及Close/Wait回收，未留自持测试服务。

日志与当时全部larksidecar源码摘要：`.local-build/lark-user-variant-proof/native-rpc-current-final/{artifact-check.json,ordinary.log,race.log,source-sha256.json}`。本包source在此检查期间未写，供server/watch独立核对稳定窗；App/local实际接口与装配仍由其各自owner维护，不按队列里的旧草案重命名。
