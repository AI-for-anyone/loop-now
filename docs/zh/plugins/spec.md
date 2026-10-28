# 插件规范

插件的权威契约随仓库代码一起版本化：

- [`plugins/SPEC.md`](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/SPEC.md)——完整规范：包格式、`package.json` 清单（`pi` 与 `loopNow` 两段）、事件目录、Hook 种类与超时、权限、身份代管和安装。
- [`api/plugin-host-protocol.md`](https://github.com/AI-for-anyone/loop-now/blob/main/api/plugin-host-protocol.md)——服务端与共享 Node 插件宿主之间的 stdio JSON 行线路协议。
- [`plugins/sdk/README.md`](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/sdk/README.md)——`@loop-now/plugin-sdk` 参考：带类型的事件名、带重试与 HMAC-SHA256 签名的投递库、状态与密钥访问、配置辅助。

## 快速参考

### 清单字段

| 字段 | 声明内容 |
| --- | --- |
| `pi` | Agent 半边：Pi 扩展、Skills、MCP 服务器、CLI——标准 Pi 包字段。 |
| `loopNow.id` | 稳定的插件标识。 |
| `loopNow.platform` | 平台半边入口文件，加载进共享宿主。 |
| `loopNow.events` | 平台半边订阅的事件目录条目。 |
| `loopNow.permissions` | 插件可调用的 API 范围（`state`、`inbox.send`、`identities.manage` 等）。 |
| `loopNow.configSchema` | 普通 JSON Schema，渲染为插件的配置表单；`x-secret` 字段只写，`x-scope: project` 字段可按项目覆盖。 |
| `loopNow.accounts` / `mcpServers` / `cli` / `buttons` / `http` / `schedules` / `channels` | 可选的平台能力，每一项都以清单声明为准。 |

### 校验

安装或发布之前，用宿主校验插件目录：

```sh
node plugins/host/dist/main.js validate <dir> --load
```

它会报出清单错误，以及代码在 `loopNow` 声明之外发起的订阅或 API 调用。

## 滚动发布

观察类事件、状态、密钥、配置和投递库现已可用。拦截与修改 Hook、控制台操作按钮、HTTP 入口、定时任务、渠道和插件账号已在规范中定义，正在滚动上线——各能力的当前状态以规范文档为准。
