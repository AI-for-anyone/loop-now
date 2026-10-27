# Lark 用户运行变体与私有 broker

**当前接线以[权威manager/PATH记录](managed-launcher-verification.md)及014工件为准。** 双loopback+probe v2及实际PATH/native已由012新增定向验证；013收注册生命周期，014仅修并发Seal取消等待并通过单项复验。旧单IPv4失败及010描述符记录保留历史，不是当前入口。App/PG/fakePi、部署与真实授权证据分别记录，不将一层绿冒充全部闭环。

本目录是 `add-lark-user-login` 的独立源码／隔离验证切片。它不是官方 Release，不是登录 helper，不启动真实 OAuth，也不修改旧 bot 发行包。当前只验证 darwin-arm64，其他平台明确不支持。

## 固定来源与构建

- 官方 commit：`f065bf5b645af381f9b7475ce721451e6ca36a23`，对应 CLI 1.0.94。
- 官方源码归档 SHA256：`997aaddb0e74cc17903ee463c9bd6554ee671d7bdd5e0aa58343e74219ee46dd`。
- 平台工件：`lark-cli-user-1.0.94-loop-authsidecar.1`；Go 1.27.1、CGO=0，仅开启官方 `authsidecar` tag，未修改官方源码。
- `build.py` 仅用于发行阶段。它可使用明确给出的归档，或下载固定官方 codeload URL；验证摘要和安全成员后构建，固定工具链、禁现场 VCS 探测、`-mod=readonly`，记录 go.mod/go.sum、实际依赖 buildinfo、平台 broker 源码摘要及工件摘要。
- 输出包含 binary、官方 LICENSE、manifest 的可追溯归档；包摘要是平台本机计算值，不冒称厂商发布校验值。运行机 installer 应使用固定归档及摘要，不拉源码／编译器；installer 由 server 另行集成。

```sh
python3 scripts/lark-user-variant/build.py \
  --archive .local-build/cli-verification-native-fixture/lark-fixed-source.tar.gz \
  --out .local-build/lark-user-variant-proof/新的独占构建目录
```

## Go 集成 ABI

`internal/server/larksidecar.Start(ctx, Binding, TokenSource, Limits)` 创建该消费者私有的随机 loopback 端口和随机能力。`Binding` 来自可信捕获，含 project/account/version/Agent/runtime、app/user/brand；不从 CLI header、query 或 argv 选择账号。

`TokenSource.AccessToken(ctx)` 必须由 server 以该确定 Binding 的闭包适配 PG 协调器：只返回同一授权版本、已经成功提交可见的 token；不能读 current。等待取消只结束等待，已开始轮换和持久化由协调器独立有界收尾。本包不实现 PG，不把合成 Source 测试冒充 PG 验证。

`Broker.Command(ctx, LaunchConfig{Binary, BinarySHA256, Home, WorkingDir, InputRoots, ProbeBinary, ProbeSHA256}, args...)` 返回未启动的 `*exec.Cmd`：验证摘要、独立私有当前 uid HOME、拒宿主 HOME／符号链接、构造不继承服务器环境的 user-only 配置。它强制使用 darwin-arm64 `sandbox-exec`，只允许自己的 broker 端口；拒绝系统钥匙串进程并禁止读宿主默认 Lark／Keychain 路径。不能提取 env 后裸启 binary 绕过 launcher。调用方负责 Start/Wait、进程生命周期以及退出时 `Broker.Close()`；上层父退出治理仍由执行器集成，不以本组件测试冒称完整 App 已接入。

Pi内部bash实际调用PATH时，使用独立Go shim `bin/lark-cli`→同scope私有exec RPC。配置、HMAC、TokenSource及真实consumer计数留服务端manager，CLI只有独立exec URL/key，不读取旧scope描述符决定native/root/env。完整当前ABI、独立SHA与证据见[managed-launcher-verification.md](managed-launcher-verification.md)；旧[launcher-abi.md](launcher-abi.md)仅历史。

允许本阶段的 `api`／`im` 业务入口，拒绝 auth/config/profile 等管理入口、身份改写参数及 bot 身份。私有 v1 转发只允许 user_info、消息列表／获取／文本发送及回复所用路径与方法；不是任意 Lark 代理。四项最小 scope 不保证所有 shortcut：reaction、p2p、媒体等附加 API 仍可能明确失败，不能自动扩权或换身份。

## 有限输入与协议边界

默认请求 1 MiB、响应 16 MiB、总时长 30 秒、空闲 5 秒、并发 8、修改签名缓存 4096 项，可由可信配置调整。请求在验签前有界读取，修改请求在取 token／转发前登记；缓存满拒绝，不淘汰仍有效的防重放记录。v1 同秒完全相同的合法修改也会被拒绝；去重不等于跨重签名业务幂等。

HMAC 使用官方 raw 环境字符串字节及原 RequestURI（含 raw query），窗口 ±60 秒。目标只允许对应品牌固定 HTTPS origin；禁止用户信息／非标准端口／目标内嵌 query／管理 token 路径。只注入 user Authorization，上游禁环境代理及重定向，不复制客户端路由／认证／任意 header；未知 POST 结果不重试。响应增量转发，超限／截断／空闲超时关闭流，不伪造完整成功。

官方 CLI 本身会先无界预读 body。生产 launcher 因此在启动前有界读取 JSON `--data`／`--params` 的 `@file` 为 argv 快照，并限制总参数字节；拒绝无限 stdin、multipart 及本阶段未支持的媒体文件入口。不承诺任意流式上传或 SDK 内存硬配额。2 MiB 原生 stress 专门在合成 fixture 中绕过该输入准入，保留同形网络隔离，只验证官方预读后 broker 413；不能以它证明未限额的 CLI 有界。

官方无 sentinel／body-read-error 可能走 base transport。因此生产 launcher 的网络拒绝规则是必要接缝。`body_fault_test.go.txt` 是对官方 middleware 的测试专用 Go overlay，不改变 release CLI 源码或 binary；它对自持目标复现回落，并验证精确生产 profile 阻断，不以此冒称从真实 CLI 命令可注入任意底层 Reader 故障。

## 验证

显式传入已核 manifest 的平台 binary 才运行 native 测试；未提供时 native 测试 skip，不能计门禁通过。测试对每个 native 启动先证明同一 production profile 允许指定自持端口、拒绝另一个自持端口，不探测外部地址。broker 的测试 TLS dialer 为包私有，仅将固定官方 URL 映射到自持 TLS fixture，生产 Start 不暴露 fixture／不安全 TLS 开关。

故障探针编译使用固定源码的 `internal/transport/platform_body_fault_test.go` 虚拟路径到本目录 `body_fault_test.go.txt` 的 Go overlay，`go test -c -tags=authsidecar -overlay <映射JSON> ./internal/transport`；不写官方源码目录。详见 verification.md 与 `.local-build/lark-user-variant-proof/body-overlay.json`。

```sh
LARK_USER_VARIANT_BINARY="$PWD/.local-build/lark-user-variant-proof/build-006/lark-cli" \
LARK_BODY_FAULT_PROBE="$PWD/.local-build/lark-user-variant-proof/official-body-fault-probe" \
  go test -race ./internal/server/larksidecar -count=1 -v
```

以上旧矩阵增量工件为 build-006（binary与005相同；manifest更新FIFO输入修复后的平台launcher摘要），归档SHA见verification.md末节。旧005/006保留历史，不覆盖；当前真实PATH入口使用012，见本文首段链接。旧原生0.2增量还需显式设置 `LARK_OFFICIAL_RELEASE_ARCHIVE` 与 `LARK_ROUTE_FAULT_PROBE` 才运行对应格；未设置的skip不能计通过。固定源码middleware overlay可编译新 `TestPlatformNoSentinelChild`，仍仅测试自持目标、无CONNECT／私有CA或生产fixture开关。
