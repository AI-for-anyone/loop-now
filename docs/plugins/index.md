# Plugin system overview

**New tools and project-specific behavior belong in plugins.** A plugin is one directory whose `package.json` is the manifest. It has up to two halves, and either one is optional:

- The **agent half** is a standard [Pi package](https://pi.dev/packages): Pi extensions, Skills, MCP servers, and CLIs, loaded into the Pi process of each agent you grant. An existing Pi package installs unchanged.
- The **platform half** runs in one long-lived Node plugin host next to the server. It subscribes to platform events and calls the platform API with a scoped plugin token.

## What the host provides

| The host provides | What it means for a plugin |
| --- | --- |
| **Events** | Every observation event in the catalog: runs, tasks, nodes, Inbox, questions, sleep, agents, Pi processes, folders, plugins. Events are persisted, acknowledged after the handler returns, retried, and replayed after a restart. Interception, modification, and claim hooks have their own timeouts and fallbacks. |
| **State + secrets** | Per-plugin persistent state, global or per project. `x-secret` configuration fields are write-only in the console and masked in plugin logs. |
| **Configuration forms** | `loopNow.configSchema` is plain JSON Schema; the console renders it. Fields marked `x-scope: project` can be overridden per project. |
| **Reliable outbound HTTP** | The SDK's delivery library keeps a persisted queue, retries with backoff, signs requests with HMAC-SHA256, and writes a delivery log that the plugin page displays. |
| **Logs + isolation** | Each plugin's output lands in its own log. A plugin that throws, times out, or fails to load affects only itself. |

The manifest is authoritative. Subscribing to an event, or calling an API scope, that the manifest does not declare fails, and install shows the user both halves, the events, and the permissions before anything runs.

## Install once, enable per project, grant per agent

Install from the official plugins, a Git repository, an npm package, or a local directory, in the console or with `loopctl plugin install`. Next, enable the plugin in a project and fill in its configuration. Then grant it to the agents that should load its agent half.

Installing or enabling a plugin never authorizes an agent by itself. Versions are pinned: updating is an explicit action, and running tasks keep the files they started with.

Two official plugins ship with the platform:

- [**webhook**](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/official/webhook/README.md) POSTs `inbox.received` events to your URL with a signature, retries, and a delivery log. External identities use it instead of polling their Inbox.
- [**hello**](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/official/hello/README.md) is the smallest plugin with both halves, and the starting point for writing your own.

## A platform half in a few lines

```ts
import { createDelivery, type PluginAPI } from '@loop-now/plugin-sdk'

export default function register(api: PluginAPI) {
  const delivery = createDelivery(api)
  api.on('task.completed', async (event) => {
    const { url } = api.config.get<{ url?: string }>(event.projectId)
    if (url) await delivery.deliver({ target: url, body: event, eventId: event.id, eventType: event.type })
  })
}
```

```json
{
  "name": "task-notifier",
  "version": "1.0.0",
  "keywords": ["loop-now-plugin"],
  "peerDependencies": { "@loop-now/plugin-sdk": "*" },
  "loopNow": {
    "id": "task-notifier",
    "platform": "./platform.ts",
    "events": ["task.completed"],
    "permissions": ["state"],
    "configSchema": { "type": "object", "properties": { "url": { "type": "string", "x-scope": "project" } } }
  }
}
```

The host loads TypeScript directly, as Pi does, and provides `@loop-now/plugin-sdk` itself. Check a directory with `node plugins/host/dist/main.js validate <dir> --load`, which reports manifest errors and any subscriptions the code makes outside `loopNow.events`.

## Trust model

There is no sandbox: plugins execute code, so install sources you trust. Trust comes from source grading (official → market-reviewed → arbitrary git/npm/local), the pre-install preview of both halves and permissions, and a one-click global disable.

## Going deeper

- [Specification](/plugins/spec) — the full contract: package format, event catalog, hook kinds and timeouts, permissions, and installation.
- [Publishing to the market](/plugins/publish) — submit your plugin to the official market index.
- [SDK reference](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/sdk/README.md) and the two official plugins in the repository.
- [Plugin host protocol](https://github.com/AI-for-anyone/loop-now/blob/main/api/plugin-host-protocol.md) — the wire protocol between the server and the host.
