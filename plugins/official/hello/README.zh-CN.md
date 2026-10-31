# Hello 示例

[English](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/official/hello/README.md)

最小的官方插件，随 Loop Now 发布，用来看插件的两半各做什么，也可以用来确认你这套平台上插件能正常工作：

- **平台半边**：启用了它的项目里，每当 Agent 的一次回复结束，就在插件日志里写一行。
- **Agent 半边**：授权给的 Agent 多一个 `hello` 工具，用来向某人问好，开头的问候语可以配置。

它不需要任何权限、账号或外部服务，除此之外什么也不做。装上试一试插件，用完卸载即可。

## 1. 安装

控制台打开 **插件市场**，在 **随平台发布的官方插件** 里找到 **Hello 示例**，点 **安装**，看过预览后确认。或者用命令行：

```sh
loopctl plugin install hello
```

## 2. 配置（可选）

插件只有一个配置项，在它的 **配置** 页（**插件市场** › **Hello 示例** › **详情** › **配置**）。表单上的名称来自插件清单。

| 字段 | 表单上的名称 | 默认值 | 作用 |
|---|---|---|---|
| `greeting` | 问候语 | `Hello` | `hello` 工具回复时开头用的词。可以按项目覆盖。 |

某个项目想用别的问候语，在该项目的插件页 **这个项目的配置** 里填。改了问候语后，Agent 下次启动 Pi 时生效。

## 3. 在项目里启用并授权给 Agent

1. **项目设置** › **插件** › **管理项目插件**：启用 **Hello 示例**。
2. Agent 配置的 **插件权限** 一栏：授权 **Hello 示例** 并保存。

授权在该 Agent 下次启动 Pi 时生效，不打断当前回复。平台在 Agent 空闲一段时间后（默认 10 分钟）回收它的 Pi，之后的第一条消息会重新启动。

## 使用

- 对 Agent 说，例如：“用 hello 工具向 Ada 问好。”它会调用 `hello`，得到 `Hello, Ada!`；配置了问候语时 `Hello` 换成你的问候语。
- 项目里任何一次回复结束后，打开 **插件市场** › **Hello 示例** › **详情** › **日志**，能看到这样一行：`Agent <Agent id> 的一轮结束：completed，用了 1234 个 token`。状态是 `completed`、`failed` 或 `interrupted`。**事件** 页列出插件收到的每个 `run.ended` 事件。

## 常见问题

- **Agent 没有 `hello` 工具**：确认授权已保存，并且该 Agent 的 Pi 此后重新启动过（见第 3 步）。
- **日志里没有记录**：插件须在该 Agent 所在的项目里启用，别的项目的事件不会送到它。**插件市场** 页还显示插件宿主的状态，平台半边在宿主里运行；如果提示服务端没有插件宿主，说明服务端启动时没有设置 `LOOP_NOW_PLUGIN_HOST_ENTRY`，这时 `hello` 工具照常可用，但不会写日志。
- **没有可安装的官方插件**：服务端启动时没有设置 `LOOP_NOW_PLUGIN_OFFICIAL_DIR`。
- 日志保留 7 天。卸载插件会删除它的配置、项目启用和 Agent 授权；已经在运行的 Agent 在 Pi 下次启动前仍有 `hello` 工具。

## 自己写插件

这个插件的[源码](https://github.com/AI-for-anyone/loop-now/tree/main/plugins/official/hello)是最小的起点。规则见[插件规格](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/SPEC.md)和 [SDK 说明](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/sdk/README.md)。
