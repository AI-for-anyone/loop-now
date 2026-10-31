# Hello example

[中文](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/official/hello/README.zh-CN.md)

The smallest official plugin. It ships with Loop Now to show what the two halves of a plugin do and to check that plugins work on your installation:

- **Platform half:** whenever an Agent's reply ends in a project where the plugin is enabled, it writes one line to the plugin's log.
- **Agent half:** Agents you grant it to get a `hello` tool that greets someone, starting with a greeting you can configure.

It needs no permissions, accounts or outside services, and does nothing beyond this. Install it to try plugins out, and uninstall it when you are done.

## 1. Install

In the console, open **Plugin market**. Under **Official plugins that ship with the platform**, click **Install** on **Hello example**, check the preview, and confirm. Or with the CLI:

```sh
loopctl plugin install hello
```

## 2. Configure (optional)

The plugin has one setting, on its **Config** tab (**Plugin market** › **Hello example** › **Details** › **Config**). The form labels come from the plugin's manifest and are in Chinese.

| Field | Label in the form | Default | What it does |
|---|---|---|---|
| `greeting` | 问候语 | `Hello` | The word the `hello` tool starts its reply with. Can be overridden per project. |

To use a different greeting in one project, set it on that project's plugin page under **This project's config**. A changed greeting reaches an Agent the next time its Pi starts.

## 3. Enable it in a project and grant it to Agents

1. **Project settings** › **Plugins** › **Manage project plugins**: enable **Hello example**.
2. In an Agent's config, under **Plugin grants**: grant **Hello example** and save.

Grants take effect the next time that Agent's Pi starts; the current reply is not interrupted. The platform stops an idle Agent's Pi after a while (10 minutes by default), and the next message starts a new one.

## Use it

- Ask the Agent, for example, "Use the hello tool to greet Ada." It calls `hello` and gets back `Hello, Ada!`, or your greeting in place of `Hello`.
- When any reply in the project ends, open **Plugin market** › **Hello example** › **Details** › **Logs**. You will see a line like `Agent <Agent id> 的一轮结束：completed，用了 1234 个 token` ("Agent …'s reply ended: completed, 1234 tokens used"). The status is `completed`, `failed` or `interrupted`. The **Events** tab lists each `run.ended` event the plugin received.

## Troubleshooting

- **The Agent has no `hello` tool:** check that the grant was saved, and that the Agent's Pi has started again since then (see step 3).
- **No log lines:** the plugin must be enabled in the Agent's project; events from other projects do not reach it. The **Plugin market** page also shows the state of the plugin host, which runs platform halves. If it says the server has no plugin host, the server was started without `LOOP_NOW_PLUGIN_HOST_ENTRY`; the `hello` tool still works, but nothing is logged.
- **No official plugins to install:** the server was started without `LOOP_NOW_PLUGIN_OFFICIAL_DIR`.
- Logs are kept for 7 days. Uninstalling the plugin deletes its config, project enablement and Agent grants; Agents that are already running keep the `hello` tool until their Pi next starts.

## Writing your own plugin

This plugin's [source](https://github.com/AI-for-anyone/loop-now/tree/main/plugins/official/hello) is the smallest starting point. The rules are in the [plugin specification](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/SPEC.md) and the [SDK reference](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/sdk/README.md).
