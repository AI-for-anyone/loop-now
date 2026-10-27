# Pi 1.1.0 认证桥接协议 v1（server 已核对运行刷新入口）

独立 `main.mjs`，一进程一操作；实际安装包必须为 coding-agent/pi-ai 1.1.0。Go `Command` 使用绝对 Node/entry、已建 0700 专用 HOME、独立 tmp、基础 PATH、空环境与 production。stdin 不参与；fd3 输入、fd4 输出 UTF-8 JSONL（每条1MiB、最多32条待写）；stdout/stderr 丢弃，不用于协议或日志。输出从首个未排空写入起有独立drain预算（默认5秒，Go ProcessOptions.DrainTimeout可传LOOP_NOW_AUTH_HELPER_DRAIN_MS），即使终态已生成也不无限等背压；到期退出码2，刷新中断由协调器按不确定结果处理。server 不得记录帧或供应商异常。进程/管道/临时目录回收由调用方负责，不能借此改旧服务。

## 输入与输出

- login可输出独立安全诊断帧 `{type:"diagnostic",id,stage,category?,httpStatus?}`；失败result可附同结构的 `diagnostic`（不含type/id）。stage固定为 provider_loaded/exchange_started/exchange_http/exchange_json/credential_received/credential_validated/result_queued，category固定为 network_error/timeout_error/tls_error/http_error/json_error/credential_error/unknown_error；HTTP必须整数100..599。exchange_json额外只允许字段存在/类型布尔，Go不将它们记日志。不得包含URL、code、token、响应正文或原异常；Go二次白名单，只在失败日志保存安全投影，API保持原错误码。refresh不输出这些诊断帧。

- 输出 ready：`{type:"ready",protocolVersion:1,expectedPiVersion:"1.1.0"}`；仅声明桥协议，不表示包已验证。
- 输入 `{type:"login"|"refresh",id,provider:"openai-codex",timeoutMs,piPackageRoot,credential?}`；id 为1–100位字母数字/下划线/短横线，timeoutMs 为正整数（不超过Node定时器范围）。包路径为服务端已配置的实际安装根目录，不从Agent工作目录解析。credential 仅用于refresh。
- 输出 notify：`{type:"notify",id,event:{type:"auth_url",url}}`，仅固定供应商授权URL；不得将它记日志。普通进度消息固定文字，原provider文本/异常/body不传。
- 浏览器模式自动选择 browser，不启用device code。需要手工回调时输出 `{type:"prompt",id,promptId,kind:"manual_code",message}`；输入 `{type:"prompt_reply",id,promptId,value}` 只能是含匹配state/code的完整 `http://localhost:1455/auth/callback` URL，不接受无state授权码。prompt_closed 撤销输入，迟到reply忽略。该秘密交互暂不等于Web已提供手工输入界面。
- 输入 `{type:"abort",id}`；输出 result 恰好一次：`{type:"result",id,status:"succeeded"|"failed"|"cancelled"|"expired"|"protocol_error",code?,credential?,cancelRequested?,protocolFault?}`。成功credential固定为 `{type:"oauth",access,refresh,expires,accountId}`，expires为Unix毫秒，单份credential JSON最多64KiB；只在fd4秘密路径交付，不进状态API。错误码固定，不包含原异常或凭证。
- login 取消/超时清理本进程回调监听及socket，抑制迟到结果。端口1455预检之外，独立辅助进程观察实际Pi callback监听的EADDRINUSE，禁止Pi吞错退manual伪成功；不终止占用者。

固定Pi1.1.0的base64url/整数过期时间兼容仅在私有helper中实施，机制与批准边界见compatibility.md；不修改安装包或放宽Go凭证契约。callback200先于exchange，不能用成功页面代替凭证保存成功。

## 刷新不可当作可撤回操作

server 按进程身份绑定的授权版本协调，不传Agent/task/grant选择或旧令牌作为授权依据。刷新一旦调用provider，普通abort/EOF只记cancelRequested；坏帧/第二命令等协议故障再标protocolFault并停止读取输入，在请求指定的独立受限预算内继续等待，将新credential交付协调器保存；server不能因HTTP调用者取消而杀辅助进程或丢弃结果。超时/中断/供应商轮换后响应丢失用refresh_uncertain明确失败，不声称未执行或旧refresh可重试。协调器须持久记录inflight/不确定状态、在独立context加密原子回写后再响应等待者；这是server接线/恢复职责，helper组件本身不证明PG已完成。

## 直接 Pi RPC 刷新接线（私有管道，与helper进程独立）

`runtime-refresh.mjs` 是唯一显式平台扩展；保持直接Pi CLI `--mode rpc --no-extensions -e <固定路径>`，不加载用户/项目自动发现扩展。stdin/stdout仍为原Pi RPC；额外fd3=服务端→Pi刷新响应，fd4=Pi→服务端刷新请求。每个Pi自己的管道由父进程绑定快照授权版本；不复用helper的管道或实例。

请求 `{id,method:"refresh",provider:"openai-codex"}`，不含grant/Agent/task/旧令牌，父进程拒绝额外授权选择字段；响应 `{id,ok:true,credential}` 或 `{id,ok:false,code}`，仅管道携带秘密，HTTP不返回token。服务端验证存活进程身份及绑定版本；扩展检查返回accountId未改变，不用当前Agent新授权替代旧任务授权。

两端UTF-8 JSONL每条1MiB，客户端最多32个待处理请求及32条尚未排空写入，写入drain也有独立同等预算，不因请求已超时就继续无限积压；响应按id匹配，迟到结果忽略，非法/超限帧或失联使当前等待明确失败。每次刷新等待默认15秒，可由服务端非秘密运行参数LOOP_NOW_AUTH_REFRESH_TIMEOUT_MS配置；取消仅停止本地等待，不向服务端发送取消轮换。服务端继续独立受限刷新并加密保存。通道不可用/超时错误固定文字，不含frame/原provider错误；Pi不能静默转回原供应商refresh。跨进程锁/版本/不确定状态由Go协调器持有。

授权URL仅console-only当前登录操作视图可返回供打开/恢复，必须固定HTTPS issuer/path、禁token/code查询项；终态清除，不进AgentDetail、日志或审计。它与access/refresh token不同。

离线helper、实际Pi显式扩展加载、Go授权版本/PG回写恢复、真实供应商/模型分别验收；参考Main refresh-acceptance.md，不因单函数测试勾1.2/3.2。

## 随 binary 部署

Go `Install(root)` 嵌入根目录*.mjs，不依赖repo cwd或测试目录。server传入已规范化工作根下的 `platform/auth-helper-v1.1.0` 绝对路径；逐层拒symlink，安装根及直接父目录须0700（缺少时创建，现有公开目录不自动chmod）。资产以0600、Sync+Rename逐文件原子替换，返回固定main.mjs与runtime-refresh.mjs；错误时不得启动入口，保留无关文件。legacy-anthropic虽随资产打包，当前native验收暂停，不能因文件可安装就启用它或声称兼容通过。

## 旧 Anthropic 字段的限定兼容入口

`legacy-anthropic.mjs` 仅由平台对旧snapshot credential.env显式-e加载；`LOOP_NOW_PI_PACKAGE_ROOT`为非秘密固定安装路径，coding-agent/pi-ai都核1.1.0。它包装原apiKey.resolve，在每调用内存AuthContext映射已知AUTH_TOKEN/OAUTH_TOKEN/API_KEY，保留原resolver及原生API行为，不写process.env。未知env项拒绝，proxy不据此兼容，新标准credential.key无需加载该扩展。

重构后的native SDK探针已在CLI初始化前的process sandbox、固定本机endpoint和最终URL前置断言下验证6种合成场景；仅本机SDK路径通过，真实供应商/迁移/任务回放另记。旧HTTPS/preload测试体已替换，沙盒不可用时fail停止，不退回旧保护方式。
