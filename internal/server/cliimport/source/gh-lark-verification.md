# gh2.100.0 / Lark1.0.94 固定 plaintext 来源续段

2026-10-16，任务2.1/2.2独占source组件切片。Main已审固定格式/最小分支，并明确：完整appId/plain appSecret/brand即为可导入应用/Bot凭证，不因同配置defaultAs=user/auto或带users而隐藏；用户OAuth另行unsupported，不提取/转换用户token。

## 格式与实现

新增 `gh.go`、`lark.go`、`gh_lark_test.go`；仅在本包 `pg.go` 的 Discover/Extract 分派表增加两分支。New/SetMySQLTools/Candidate/Selection/Extract/Recheck/WithOrigin公开签名未改，原主文件指纹/可信builtin renderer/私有callback/error/clear机制复用。未改依赖清单、API/HTTP/store/Tx/迁移、loopctl/guide。

GH默认固定 `.config/gh/hosts.yml`。明文格式：github.com下 users.<精确user>.oauth_token；legacy没有users时必须明确顶层user+oauth_token。users存在时不猜顶层active token回退，也不按配置顺序选人；空users但顶层token存在明确unbound_identity。缺明文/未绑定keyring为keyring_version_unbound，不尝试解锁/读取最新值。仅github.com；Enterprise或api_host映射无法无损表达为当前GH_TOKEN模板，明确unsupported。环境/自定义config根不继承、不跑gh auth token/status或任何native命令；不会自动切用户、登录/增scope或联网查询token主体。

这导入的是选中的静态文件载荷，saved/未远端验证；不声称文件user已经供应商确认，也不声称已解析真实keyring优先级或宿主当前活动authority。sourceOrigin固定host/user的私有selector，不按用户名合并既有项目授权。

GH YAML使用当前已锁依赖的Node解析，拒绝所有显式tag、anchor/alias/merge、重复键、多文档、非字符串身份/secret、未知字段，限制节点/深度，原始解析错误安全化。通用解析器会短暂materialize混合文件里的scalar字符串，不能夸称内部/OS没碰秘密；AST不进入scan/Selection缓存，发现只返回已校验host/user元数据，只有精确选择分支才投影token字段，随后清AST value/comments及byte buffer；Go string物理擦除仍无法保证。

Lark固定 `.lark-cli/config.json`，支持1.0.94 MultiAppConfig.apps，每条完整appId/plain appSecret/brand按当前正式模板映射，品牌必须显式feishu/lark，不套默认掩盖缺项。应用/Bot标签与Lark用户登录候选分开；userOpenId/name不进入普通标签/缓存，用户token绝不作为appSecret。appSecret的file/keychain/其他SecretRef保持准确unsupported、不解引用，不把对象stringify为literal secret。file引用未纳入本plaintext切片，keyring无可证明代际仍拒绝。

Lark JSON按字段流式读取：重复/未知字段、secret对象/用户token混入、坏品牌/缺secret/坏类型等固定安全失败；第一遍仅安全metadata与存在位，选中index+appId绑定后第二遍才解码该条appSecret，不解码非选条目secret。同配置中的profile/default身份不被复制为平台执行模式；只保存明确选中的Bot凭证，沿既有renderer注入strict bot。不导入用户OAuth或扩大Agent权限。

两类plaintext选后要求源不被group/world读取；Supplement任何非空均拒绝，禁止借数据库补项覆盖身份。源before/after与commit前Recheck复用；密钥引用、完整路径、token/secret、raw parse/native诊断不进入候选或普通响应。

## 固定源码依据

- [gh2.100.0 config.go](https://github.com/cli/cli/blob/v2.100.0/internal/config/config.go)：Login持久化users/user/oauth_token；TokenForUser有keyring分支，故本适配不调用它或假称file stat覆盖keyring代际。
- [gh2.100.0 go.mod](https://github.com/cli/cli/blob/v2.100.0/go.mod)固定go-gh2.16.0；其[config.go](https://github.com/cli/go-gh/blob/v2.16.0/pkg/config/config.go)定义默认HOME/.config/gh/hosts.yml及显式环境override。此适配仅默认受限根，无环境/任意路径输入。
- Lark1.0.94 [config.go](https://github.com/larksuite/cli/blob/v1.0.94/internal/core/config.go)、[secret.go](https://github.com/larksuite/cli/blob/v1.0.94/internal/core/secret.go)、[secret_resolve.go](https://github.com/larksuite/cli/blob/v1.0.94/internal/core/secret_resolve.go)、[workspace.go](https://github.com/larksuite/cli/blob/v1.0.94/internal/core/workspace.go)：multi-app字段、plain/string与SecretRef union、plain直接使用、file/keychain另行解引用；默认.lark-cli而非继承其他workspace。

## 必要验证

新增10顶层用例、31个定向子例；仅合成HOME。覆盖精确active/nonactive GH用户各取自己的token、legacy绑定、缺身份、Enterprise/api_host、空/缺plaintext users不猜回退、YAML graph/type/重复键/raw错误（包含users存在时顶层oauth_token仍拒绝未知对象，不忽略无损映射风险）；完整Lark Bot在有用户/defaultAs=user时仍可选、用户登录独立拒绝、各SecretRef/缺brand/缺secret/未知brand/JSON重复未知字段/用户token混入、只取选中app的secret、源变化/0600/非法补项。再用一个五来源注册回归，验证既有PG/MySQL/GoogleSA与新增GH/Lark都通过原ABI正式renderer；没有重做旧App/PG/原生工具矩阵。

先构建测试binary，再对binary施加：

```
go test -buildvcs=false -c -o <private-temp>/source.test ./internal/server/cliimport/source
/usr/bin/sandbox-exec -p '(version 1)(allow default)(deny network*)(deny process-fork)' <private-temp>/source.test '-test.run=^Test(GH|Lark)' -test.count=1 -test.v
```

race构建增加`-race`；构建环境固定GOTOOLCHAIN=local/GOPROXY=off/GOSUMDB=off，临时binary自动清理。最终ordinary全部PASS（最终binary elapsed0.784s），race全部PASS（1.884s），无skip/race；`go vet ./internal/server/cliimport/source` exit0。网络和派生进程均由测试binary的OS沙箱禁止，不是用成功函数替代adapter；两个新增adapter没有native命令依赖。早先直接把go test编译器也包在process-fork deny导致编译进程无法启动，该失败已改为先编译再约束实际测试binary，不放宽运行时策略、不隐藏历史。

## 交接边界

原三工具组件/native证据保留在README，不将新增两适配后的固定五来源支持说成所有格式/HTTP/持久化/真实host都完成。server仍持正式scan/project/instance/TTL/API/整批Tx/provenance/receipt/取消与未知提交；必须在事务外Extract并短seal/HMAC，提交前Recheck，不能用GET重提取、改投最新源或按用户名合并。

本会话不勾2.1/2.2或0.2整项。keyring provider/file SecretRef尚不支持、个人Google技术边界未变、Lark用户不转换；没有现库/服务/host/keyring/provider/业务DB/Git操作，无临时进程遗留。

### 本段输入SHA256

- `gh.go` `a2ac20d5b5529d463ee3d9cc3c135f1bb73533de9c1040ce7ea01fe7b1377f1b`
- `lark.go` `afebaa40a4a63ce8bc9cb6619942c9e12c69afa65e3d93021d195d573263496a`
- `gh_lark_test.go` `6d4750b17099e9c1f8337a6e6d141331503df1132d884de83a0eb5455c637ecf`
- `pg.go` `6763ddf2ae3edb9bbeeba305ef30de3cd3a5d4c5c71f07a443eb7c36936fe921`
