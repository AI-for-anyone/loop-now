# Anthropic 原生客户端探针隔离方案（待 Main 核对，暂停运行）

## 已观察事实

新 `anthropic-rpc.test.mjs` 的两轮调试均失败，观察到实际供应商返回 401 Invalid bearer token。使用的只有合成凭证；未读取或使用用户凭证。global fetch preload 的报告为空，说明该拦截没有覆盖实际请求，不能称零外连，也不能计离线通过。已经停止重跑并将该测试固定为 test.skip，避免普通测试命令误触发。Codex 使用测试 streamSimple 的既有零供应商尝试证据独立保留。

当前只做源码分析和写方案，不通过外部地址验证新拦截。legacy-anthropic.mjs 本身尚无实际 Pi 兼容验收；server暂不将它接入执行器。

## 固定本机端点，先证 URL 再请求

1. 在测试自有父进程创建 `127.0.0.1:随机端口` HTTP fixture，处理 `/v1/messages` 并返回静态 Anthropic SSE；不跳转、不代理、不调用外部地址。服务端只记录请求数/认证模式断言结果，不打印 header、令牌、请求正文。
2. 独立0700 HOME/PI_CODING_AGENT_DIR、0600合成auth.json；在该测试作用域 models.json 的 anthropic 配置中明确设置 `baseUrl=http://127.0.0.1:<port>`。不改生产兼容扩展的固定供应商provider或稳定Codex刷新扩展。
3. 启动实际安装Pi1.1.0 `--mode rpc --no-extensions -e <legacy固定平台扩展>`，禁Skills/prompt templates/themes/MCP/tools/session自动资源加载。**只发 get_state，不发 prompt**；断言返回model.provider、model.api及model.baseUrl精确匹配本机fixture，再允许合成prompt。
4. 静态依据：实际安装 provider-composer.js 的 applyModelsJson 会把 config.baseUrl 应用到模型；composeProvider.baseUrl 的优先级为 extension/config/base；native base.streamSimple 接收该已投影model。原Anthropic resolver不返回auth.baseUrl，因此不能覆盖已锁定的本机URL。此静态依据仍须运行get_state确认，不以推断替代。
5. fixture断言最终Host/本机监听端口、路径 `/v1/messages`（若固定SDK实际追加query，仅接受事前审查过的固定参数），以及每个case预期Authorization或x-api-key；不是仅调用自定义resolver函数。Bearer/两key/三keycase均与原resolver的纯离线结果比对，不把代理或未知字段加入兼容结论。

## 第二道保护：进程网络 deny

- 当前只确认 `/usr/bin/sandbox-exec` 二进制存在，**尚未证明其在当前系统可用或profile正确**。
- Main核对后，先用独立无SDK的小程序验证配置：拒绝所有网络出站，仅允许该fixture的127.0.0.1固定端口；不允许DNS、外部地址或通过代理出站。验证不能使用真实供应商端点，使用本机另一个未许可端口即可证明拒绝；允许端口和拒绝端口均为测试自行持有。
- 沙盒启动失败、拒绝用例不成立或运行平台不支持时，整个native SDK探针不得启动。不能静默退回global fetch patch，也不宣称进程级零外连。此时只保留纯resolver证明和本机端点fixture设计/静态证据，回Main选择可用隔离方式。
- 可保留preload作为第三层诊断，但它不再承担网络安全边界。所有子进程必须从CLI初始化前就在进程级deny内；父进程也只持有本机fixture，不执行供应商请求。

## 恢复运行门禁与证据

Main审查本方案并确认允许隔离能力验证后，先提交/核对沙盒小程序结果与本机URL配置；验证成功才解除native探针skip。实际测试需记录固定版本、模型最终URL、本机请求数、认证模式、未许可本机端口被拒绝、scope文件权限和清理；不通过实际外部地址“试是否拦住”。

该方案尚未执行，不勾兼容/刷新/实际运行任务。assets.Install的独立嵌入安装证据与此前Codex/helper证据不因此变成失败，Anthropic native兼容验收单独保持未完成。

## Main批准后的隔离能力/metadata验证（2026-10-14）

仅执行本节范围，没有运行native SDK模型请求或解除native test.skip。此前两轮意外401调试已分别如实报告；这里的成功不能将那两轮改记为离线通过，也不推断其准确请求次数。

**能力命令**：`/Users/ljl/.local/node-v24.21.0/bin/node auth-helper/test/check-sandbox.mjs`。

第一次profile使用IP字面量，sandbox解析器拒绝，exit=65：`host must be * or localhost in network address`，子Node未执行。改用解析器支持的localhost过滤后通过。两次profile与端口均保存在 `sandbox-capability-result.json`，不覆盖失败证据。

实际通过的profile：

```scheme
(version 1)
(allow default)
(deny network*)
(allow network-outbound (remote ip "localhost:52679"))
```

两个端口都由测试父进程持有、仅绑定127.0.0.1。基线端口52679/52681均返回200；sandbox子进程指定端口52679返回200，52681返回EPERM。父进程计数允许端口1、拒绝端口0，sandbox exit=0/stderr为空。没有SDK/Pi导入、没有真实外部地址探测；测试端口和临时目录已回收。只证明本机此次profile的允许/拒绝行为。

**Metadata命令**：`/Users/ljl/.local/node-v24.21.0/bin/node auth-helper/test/check-anthropic-state.mjs`。

脚本先检查上述能力通过，再用同形profile限定本次fixture端口54186（另一自持端口54187不开放），sandbox内直接启动实际Pi1.1.0 RPC与显式legacy平台扩展。只发送get_state，独立models.json把anthropic baseUrl设为 `http://127.0.0.1:54186`。实际响应：provider=anthropic、api=anthropic-messages、id=claude-fable-5、baseUrl精确等于该本机URL；两个fixture计数均0、stderr为空。没有prompt、模型客户端请求或外部地址探测，所有自持进程/监听/目录已回收。首次本地metadata脚本错误按单层读取嵌套模型目录，发生在Pi spawn之前，修正后通过；两次结果保存在 `anthropic-state-result.json`。

下一步仍需Main决定native验收范围。**不能直接解除旧native测试的skip**：旧body还是HTTPS/preload方案，必须先改成上面经证实的本机models.json+启动前sandbox+get_state断言+父进程HTTP fixture，再由Main允许模型请求测试。当前没有宣称Bearer/多key的native客户端兼容通过。

## Main批准重构后的 native SDK 本机验证（2026-10-14）

原危险HTTPS/preload测试体已完整替换，不是直接解除旧skip。新 `test/anthropic-rpc.test.mjs` 每个case都在本轮自持的两个127.0.0.1端口先做同形sandbox能力验证；成功后从Pi CLI初始化前使用同一sandbox，只允许本轮fixture端口，metadata先get_state精确断言，再发合成prompt。没有JS fetch patch，fixture只返回本机静态SSE，不代理、跳转或外连；任一preflight/URL失败即fail整个测试，不继续或降级。

实际命令 `node --test auth-helper/test/anthropic-rpc.test.mjs`（固定Node24.21.0）通过：1个顶层测试、6个case、无skip。每case均允许本机端口200、另一自持端口EPERM且0hit，最终Pi模型URL精确loopback；原生SDK向fixture恰好POST一次 `/v1/messages?beta=true`，SDK成功解析静态SSE回复。query beta=true在实际安装SDK beta/messages源码事前核实。RPC/stderr没有合成凭证值。

| case | 原生SDK请求认证模式 | 结果 |
|---|---|---|
| 单AUTH_TOKEN | Authorization Bearer，无x-api-key | 通过 |
| 单OAUTH_TOKEN，普通合成字符串 | x-api-key，无Authorization | 通过 |
| 单API_KEY | x-api-key，无Authorization | 通过 |
| AUTH_TOKEN + OAUTH_TOKEN + API_KEY | AUTH_TOKEN Bearer优先 | 通过 |
| OAUTH_TOKEN + API_KEY | OAUTH_TOKEN优先 | 通过 |
| OAUTH_TOKEN含合成sk-ant-oat前缀 | 原生API识别为Bearer | 通过 |

第六项保留原生API在resolver之后识别token前缀的行为，不将该旧字段误造为可刷新OAuth凭证。详情含实际每次profile/端口/URL/路径/认证匹配结果见 `anthropic-native-result.json`，不含header或token明文。所有测试自持进程/监听/临时目录均回收。

这仅证明固定Pi1.1.0、已知三字段兼容扩展的原生SDK本机认证路径；不证明代理、未知字段、PG迁移/旧任务回放或真实供应商账号/模型。此前两轮意外401历史仍保留，新的本机通过结果不改写它们。
