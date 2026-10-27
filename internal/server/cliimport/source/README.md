# 本机静态来源适配：内部接口与离线证据

2026-10-16。当前已补齐五工具的固定静态来源切片（GH/Lark续段见 [gh-lark-verification.md](gh-lark-verification.md)），不代表HTTP/持久保存或真实来源通过。Main 已审最小接口并准入此独占包；MySQL masked 发现与匿名私有 FD 另经 Main 源码/合成证据复核准入。来源只在显式受信 HOME 根内；本轮所有 HOME 均测试临时生成，不读取真实宿主账号。

## 内部接口（不是 HTTP DTO）

- `New(absoluteHomeRoot, catalog)`：不读环境变量或 os.UserHomeDir；catalog 对已用 CLI 必须与可信 builtin 完整模板指纹一致，包括 credential format；同名 override 不绕过检查。
- `SetMySQLTools(ctx, MySQLTools)`：发现之前一次配置，仅受信 server dependency 注入固定完整官方包 Root 及 editor/printer SHA；实际执行 version 必须为8.4.9。缺准备明确 tool_unavailable，不搜索 PATH、不借宿主工具。工具完整依赖闭包/资产校验由准备 owner 承担，此接口不把调用方任意 binary 声称为官方包。
- `Discover(ctx) []Candidate`：安全 ID/CLI/sourceKind/label/missing/reason 投影，向后兼容新增 SupplementableFields，语义与证据见 [supplementable-verification.md](supplementable-verification.md)。无路径、DSN、秘密、原始错误或摘要。未选默认由 UI/scan owner 负责；此包不产生已保存账号。
- `Select(candidateID) Selection`：仅当前 Reader 登记的私有句柄；Selection 的 JSON/formatter 拒绝/遮盖，不能用调用者路径代替 ID。
- `Extract(ctx, selection, Supplement, consume)`：只为有效选中项提取，Supplement 内部仅 Database/SSLMode/CACert，原字段非空即拒绝覆盖（即使同值）。正式 clitemplate renderer 校验后才短回调 `consume(fields map[string]string)`，交 server seal/HMAC；无 DTO/HTTP/store/事务。任何 callback 错误统一 consumer_failed，不透传秘密诊断。callback 后 clear map/byte buffers；Go string/renderer 内部复制及 OS 页缓存不可保证物理擦除。
- `Recheck(ctx, selection)`：提取前后及 server commit 前完整来源集合复核。主文件、pgpass、CA/cert/key 依赖冻结 dev/inode/size/mode/mtime/ctime，工具/catalog 同时复核；不包含读取会改变的 atime。不能宣称与宿主写事务原子。server 必须在提交前再调用，并自行保障 scan/project/instance/TTL/deadline/cancel/revision；本包不承担这些正式操作合同。
- `Selection.WithOrigin(func([]byte) error)`：server 已审私有 provenance 接缝；稳定编码 root dev/inode、CLI/sourceKind/相对源/entry/adapterVersion，排除 secret/mtime/源内容版本。仅短 HMAC callback，错误安全化，bytes 随后 clear；不加到普通 Metadata，不存裸密码 hash。

## 实现文件

`reader.go`（固定根、逐目录 NOFOLLOW FD、指纹/句柄/模板锁/提取回调/私有 origin）；`formats.go`（受限格式、补项与完整 DB 映射）；`pg.go`；`mysql.go`；`mysql_tools.go`（实际固定工具私有 capture）；`google.go`；`source_test.go`；`native_mysql_test.go`；后续新增 `gh.go`、`lark.go`、`gh_lark_test.go`，仅扩 Discover/Extract 内部注册，不改既有ABI。不修改 API/HTTP/store/migration/既有 CLI/guide。

## 实际支持与拒绝边界

| 来源 | 支持 | 明确拒绝/限制 |
| --- | --- | --- |
| PG18 service+pgpass | `.pg_service.conf` 明确 TCP host/port/user、database/sslmode；缺库补后才用明确目标按 pgpass 官方顺序取首匹配，包括通配；保留冒号/反斜线转义；pgpass0600约束。原 CA/客户端证书/私钥完整转正式字段。 | 多host/socket、prefer/allow、未知选项/动态认证、非法/加密 client key 不静默丢弃；不猜库/password/TLS/缺身份端口。依赖引用只接受显式固定 HOME 根内绝对路径；相对引用依赖未知 native CWD，准确 unsupported，不重解释为 HOME。 |
| MySQL8.4 option file | `.my.cnf` 的 client/mysql 静态有效配置、原生引号/转义、显式 TCP/TLS；两组只在无同字段重叠时组合；缺 database/TLS/公开 CA 可补。 | 重叠字段保守拒绝，不猜文件顺序覆盖；PREFERRED、动态include、init-command/未知有效选项、socket、world-writable option file、无法完整映射都拒绝；不读系统/环境默认配置。 |
| MySQL8.4.9 login-path | 官方 editor print --all 只作为 masked 元数据发现，逐 group 候选；password 行必须恰为固定 *****，异常拒绝；选后 printer --defaults-file=/dev/null --login-path=<原group> --show <原group> 私有 capture，独立于默认 client path。 | 不是整文件一选全导入。原生内部会解密/触及多个条目，不能宣称 OS 没读/内部没解密；保证非选 secret 不向平台导出。无可信固定工具/实际版本/指纹则 tool_unavailable，不自写解密。section 不得以 '-' 开头，避免作为 native flag。 |
| Google568 SA JSON | 固定 ADC 与 legacy_credentials/<account>/adc.json 内完整 service_account，经现 gcloud 模板 RSA/issuer/字段校验映射 `serviceAccountJson`。 | authorized_user 为 requires_platform_login，不复制 refresh；external/dynamic/impersonation/metadata/未知格式明确拒绝。credentials.db 与 access_tokens.db 只 stat 并区分 unsupported_cli_store/cached_token_not_credential，不加载/导出秘密；不扫描任意 SA 路径、跑 gcloud Load/describe 或刷新缓存。 |

发现混合明文文件在底层可读到含秘密的字节，不夸称 OS 层未读。解析仅记录 password 存在位而不取其值；未选 section 不解码秘密，pgpass/客户端 key 不在扫描读取，只有受限 stat。选后 pgpass 仅解码第一个匹配目标的密码。该段为首个三来源切片的证据边界；随后新增GH/Lark固定plaintext支持，见续段。未评审格式仍准确unsupported，不把固定静态支持扩成五工具所有认证形式均支持。

## MySQL 私有原生 ABI

`runTool` 最多5s（继承更短ctx），stdout/stderr 各1MiB私有 bounded buffer，stderr 从不输出。命令无 shell，参数固定；version 使用 printer --no-defaults --no-login-paths，editor 显式 MYSQL_TEST_LOGIN_FILE=/dev/null，独立空 HOME、过滤环境，不读默认登录。

真实 ABI 证明 editor print 用原 RDONLY 源 FD 传 /dev/fd/3 会失败；不能为此给原宿主文件 RW。当前实现：private0700 HOME 创建空0600 FD → 必须成功 unlink → 才写源的 obfuscated 快照 → child 只继承副本 FD。原源仍 RDONLY，完整 stat 前后复核。没有命名秘密文件/持久备份；匿名 FD 不保证 OS 物理不落页。失败/取消均关闭 FD、清私有 HOME，context cancel 杀子进程组并有界 Wait；private capture formatter 遮盖。运行前后 tool digest 复核，不把 native stdout 包成普通 DTO。

## 验证矩阵与界限

- PG first-match/wildcard/转义与 escaped '*' 字面量；缺库补后匹配；原 TLS 字段只读；源改变、pgpass权限、跨 Reader、任意路径 ID、symlink 逃逸拒绝。
- 原 CA/cert/key 全量映射及任何 key 变化 Recheck 失败；同内容/同mtime重写仍由 ctime 拒绝；短提取后源变须 commit 前 Recheck，不能靠已 seal 误报安全提交。
- MySQL quoted opaque password、两组静态映射、动态 include/PREFERRED/未知选项拒绝；mask形状/输出上限/formatter/取消与 tool错误指纹拒绝。
- Google 完整合成2048-bit RSA SA、本地正式 renderer；坏 private key、加密 client key、个人 refresh、动态来源不保存；CLI store/cache 状态不被隐藏成空列表。
- 模板 format 改动与同名 override 拒绝；8并发只读 Recheck；私有 origin 同源跨扫描稳定、源版本变化不混入origin、跨 Reader及空 Selection拒绝、callback buffer 清理。
- 实际官方8.4.9 native fixture：临时合成 alpha/beta，再加独立 client trap；masked 列出逐账号，生产 Reader 最终3候选；只选 alpha 私有提取不含 beta/default 身份，补 database/TLS后通过正式 renderer。原 obfuscated bytes 不变；没有成功替身函数或手写解密。

独立 native fixture 初轮 sandbox 同时拒绝 network 与 `/Users` 宿主读取（仅放行经审工具树）。最终整个 Go 测试/实际 Reader 子进程再由外层 sandbox 拒绝 network；为避免 macOS 禁止嵌套 sandbox，测试专用 `LOOP_SOURCE_TEST_NETWORK_DENIED=1` 表明父测试已施加策略，native fixture不二次初始化 sandbox；这不是生产 flag，也不跳过用例。

证据层仅来源组件/合成文件/实际 native 私有 ABI。不代表 console HTTP/scan TTL/持久操作/PG整批保存/取消提交竞争/真实宿主提取或部署通过。0.2 正式 API由 server 继续冻结；本会话不勾任务，不改现库/服务/真实provider/业务DB/宿主钥匙串/Git。

## 固定依据

PG18 [service](https://www.postgresql.org/docs/18/libpq-pgservice.html)、[pgpass](https://www.postgresql.org/docs/18/libpq-pgpass.html) 明确 service 合并、首匹配/通配/转义与权限，未用默认库推导目标。

MySQL8.4 [option files](https://dev.mysql.com/doc/refman/8.4/en/option-files.html)、[my_print_defaults](https://dev.mysql.com/doc/refman/8.4/en/my-print-defaults.html) 与 [8.4.9 my_default.cc](https://github.com/mysql/mysql-server/blob/mysql-8.4.9/mysys/my_default.cc)；另核固定工具包内两份 man 与实际 version，不凭命令名推定安全。

Google568实际官方SDK源码 `core/config.py:LegacyCredentialsAdcPath` 指定 legacy_credentials/<account>/adc.json；`credentials/store.py` 明确 refresh 会写 token cache，因此本包不调用其加载/刷新。SA字段最终以现正式clitemplate/google.go renderer为准，不制造第二套JSON schema。

## 运行历史（如实保留）

初轮组件/native普通及race通过；首次 vendor fixture 根路径误多一层导致SKIP，修正后实际执行通过，后续结果均非skip。真实RDONLY FD ABI失败保留为上述匿名FD修正原因。一次公共 helper重构丢失局部err变量导致编译失败，已在本独占文件修正。最终外层 sandbox 初跑因 macOS 不支持嵌套 sandbox导致 native初始化失败，测试harness移除重复初始化后实际执行绿；没有放宽网络限制、扩大生产超时、替换成功函数或隐藏失败。


## 2026-10-16 首个三来源阶段的验证/交接

暂停前最后 section leading '-' 修正已先单独验证：

`go test -buildvcs=false ./internal/server/cliimport/source -run '^TestSectionCannotBecomeNativeFlag$' -count=1 -v` → PASS1.694s。

随后仅本包完成必要最终检查（16个顶层用例，另7个 unsupported 子例；native真实执行，无skip）：

```
LOOP_SOURCE_TEST_NETWORK_DENIED=1 GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off /usr/bin/sandbox-exec -p '(version 1)(allow default)(deny network*)' go test -buildvcs=false ./internal/server/cliimport/source -count=1 -v
LOOP_SOURCE_TEST_NETWORK_DENIED=1 GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off /usr/bin/sandbox-exec -p '(version 1)(allow default)(deny network*)' go test -buildvcs=false -race ./internal/server/cliimport/source -count=1 -v
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go vet ./internal/server/cliimport/source
```

结果：ordinary PASS0.761s，race PASS2.356s，无race报告；vet exit0。实际8.4.9 native用例分别0.11/0.12s，先alpha/beta masked元数据与选alpha证明，再加入client trap，真实Reader有三个独立候选且选alpha没有继承default/beta身份。没有重做 App/数据库/全仓/真实连接矩阵。

任务映射：为1.1/1.2提供本独占静态来源切片证据；2.3完整固定JSON SA及个人阻断；2.4固定service+pgpass/TLS；2.5静态option及实际login-path私有ABI。没有自行勾这些整项，0.2/API/scan存储/project TTL/CAS/持久Tx仍由server冻结与接线验收。首阶段时2.1 gh与2.2 Lark未实施，后续静态适配证据见gh-lark-verification.md；无可信代际keyring provider直接unsupported，不在本包伪造能力。Google个人仍Main技术边界，不写成用户确认。

server接线必须持有Reader生命周期和scan/project/instance/TTL，拒绝过期/跨scope/未经选择的句柄；只在DB事务外Extract并短seal/HMAC，事务提交前Recheck完整来源集合；WithOrigin只交私有域分离HMAC，不能普通返回路径/摘要。源stat复核不等于与宿主写事务原子；出现变化要求重扫，不自动读最新源覆盖；本包也不负责已导入混批/receipt/取消提交竞争。

本轮无仍运行的自持临时操作。源码与接口作为本独占阶段稳定交接，正式API变更需要server单写并显式协调；不以组件绿冒充全change完成。

### 交接时 Go 输入 SHA256

- `formats.go` `02a5a7f13fb588522ea5d36ff6eda68bee908453e09a7a381e7b93a1636927da`
- `google.go` `ebbbb7424464821a423096c411adb2466893a1bda228a3813318a48a542f8387`
- `mysql.go` `df5de48aeaf48680b69cf34e32afa353940ed1a18bccc52f34ad8628cd0bf50b`
- `mysql_tools.go` `75adf60cbee0ec504690265c8ebacbfa0b9106e7247e72a696d6e1254e11c02f`
- `native_mysql_test.go` `b6725322367e0d190f1b6529ff1045d0e847628e6615131b1b1fe9242e866800`
- `pg.go` `31c1f40e7182957555146d95b17bbca3c9649367da940f611e71152f67910a9a`
- `reader.go` `4105ad7172af6817753f692260dc9c22b37863fed153407a7a6336a9678e7ec6`
- `source_test.go` `ed6f340ac83fa96bf70729215ae54025ee3e7a500b3a5d2ae6c1eb4fe0872195`
