# 固定 Pi 1.1.0 OAuth 兼容与安全诊断

仅用于独立 helper 的 openai-codex login/refresh；不修改安装包、其他 provider、Agent 或运行中的 Pi。用户与 Main 已批准最小已有认证修复，插件体系仍暂停。

实际安装 `pi-ai/dist/auth/helpers.js` 的 lazyOAuth 透传 login/refresh；`auth/oauth/openai-codex.js` 先接收 callback 并返回成功页面，之后才 exchange。它以 `atob(payload)` 解析 JWT，并计算 `Date.now()+expires_in*1000`。本机实际冷 lazy provider + 私有 fd + 合成 exchange 回归已复现：合法 base64url 含 `-/_` 时 atob 拒绝；小数 expires_in 可产生 Go int64 拒绝的小数毫秒。正常响应完整返回 fd4 并退出0。HTTP401及运输异常同样发生在 callback200之后。这些是可发生路径，不能据此确定用户唯一真实操作的根因。

适配机制：仅固定 provider 调用期间暂包装本进程 atob，合法 base64url 字符串中的 `-`/`_` 转为标准 base64 `+`/`/`，继续调用原 atob 校验；标准 base64及其他输入沿用原函数，非法输入仍拒绝。login/refresh成功或失败、取消均在 finally 恢复原函数；取消未结束的供应商调用仍在原有受限生命周期内完成。账号提取、state、PKCE、账号一致性仍由原 Pi/helper 执行，不改签名令牌内容。

输出 expires 先验证有限、正数、不超过 Number.MAX_SAFE_INTEGER，再向下取整且结果必须为正安全整数；不延长有效期，不放宽 Go canonical int64。

安全 diagnostic 只走私有 IPC，字段为固定 stage/category、HTTP整数状态及字段存在/类型布尔；不含 URL、code、token、响应正文或原异常。stage 为 provider_loaded/exchange_started/exchange_http/exchange_json/credential_received/credential_validated/result_queued。category 为 network_error/timeout_error/tls_error/http_error/json_error/credential_error/unknown_error；成功可省略。Go二次白名单且日志只记录失败的安全投影。实际供应商连接、真实账号与服务切换由 Main 单独验收。
