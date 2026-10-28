# Specification

The canonical plugin contract lives in the repository and is versioned with the code:

- [`plugins/SPEC.md`](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/SPEC.md) — the full specification: package format, the `package.json` manifest (`pi` and `loopNow` sections), the event catalog, hook kinds and timeouts, permissions, identity management, channels, and installation.
- [`api/plugin-host-protocol.md`](https://github.com/AI-for-anyone/loop-now/blob/main/api/plugin-host-protocol.md) — the stdio JSON-lines wire protocol between the server and the shared Node plugin host.
- [`plugins/sdk/README.md`](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/sdk/README.md) — the `@loop-now/plugin-sdk` reference: typed event names, the delivery library with retries and HMAC-SHA256 signing, state and secrets access, and config helpers.

## Quick reference

### Manifest sections

| Section | Declares |
| --- | --- |
| `pi` | The agent half: Pi extensions, Skills, MCP servers, CLIs — standard Pi package fields. |
| `loopNow.id` | Stable plugin identity. |
| `loopNow.platform` | Entry file of the platform half, loaded into the shared host. |
| `loopNow.events` | The event catalog entries the platform half subscribes to. |
| `loopNow.permissions` | API scopes the plugin may call (`state`, `inbox.send`, `identities.manage`, …). |
| `loopNow.configSchema` | Plain JSON Schema rendered as the plugin's configuration form; `x-secret` fields are write-only, `x-scope: project` fields can be overridden per project. |
| `loopNow.accounts` / `mcpServers` / `cli` / `buttons` / `http` / `schedules` / `channels` | Optional platform capabilities, each gated by the manifest. |

### Validation

Before installing or publishing, validate a plugin directory against the host:

```sh
node plugins/host/dist/main.js validate <dir> --load
```

This reports manifest errors and any subscriptions or API calls the code makes outside what `loopNow` declares.

## Rolling out

Observation events, state, secrets, configuration, and the delivery library are available now. Interception and modification hooks, console action buttons, HTTP endpoints, schedules, channels, and plugin accounts are specified and being rolled out — check the spec for the current status of each capability.
