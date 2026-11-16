# Plugin host protocol (version 1)

The stdio protocol between the Go server and the plugin host (a long-running Node process); it complements
`plugins/SPEC.md` §3. The Go side is implemented in `internal/server/pluginhost`; the fake host used in tests is in
`internal/server/pluginhost/fakehost_test.go`.

## Process

- Go starts the host as `node <host entry>` with the working directory `<work root>/platform/plugins/`, in its own
  process group; when it exits or is stopped, the whole process group ends with it.
- **Standard output carries protocol lines only.** A plugin's `console.log` and similar output must go to standard error
  or to `log` messages; otherwise it breaks the protocol.
- Standard error goes line by line into the platform log and the plugin log (level warn). Lines produced by a plugin are
  prefixed with `[<plugin id>] `, which Go uses to attribute them to that plugin; lines without a prefix belong to the
  host itself.

## Line format

One JSON object per line, UTF-8, terminated by `\n`, at most 16 MiB per line:

```json
{"id": "g12", "type": "load", "plugin": "webhook", "payload": {}}
```

- `id`: requests are numbered by their sender and unique within that direction (Go uses `g<n>`, the host should use
  `h<n>`); the response to a request carries the same `id`. Messages that need no response (`ready`, `log`, `shutdown`)
  carry no `id`.
- `plugin`: the id of the plugin the message is about; omitted for messages about the host itself.
- Response payloads are always `{"ok": true, "value": <anything, optional>}` or `{"ok": false, "error": "<reason>"}`.

## Startup and ready

1. After starting, the host writes `{"type":"ready","payload":{"protocol":1,"hostVersion":"<version>"}}` as its first
   line. Go sends nothing before `ready`; no `ready` within 30 seconds, or a `protocol` other than 1, counts as a failed
   start.
2. Go then sends `load` to each plugin that has a platform side, in order, waiting for each `result` before sending the
   next.

## Go → host

| type | Payload | Host response | Go's time limit |
|---|---|---|---|
| `load` | See below | `result`: ok once `register` returns (including asynchronous completion); ok false with the reason in `error` when it throws, or when its subscriptions or permissions go beyond the manifest | 30 seconds |
| `unload` | `{}` | `result`: ok once the lifecycle has stopped and every registration is released | 30 seconds |
| `config_changed` | `{"config": <same as load.config>}` | `result` | 30 seconds |
| `event` | `{"event": <event object>, "attempt": n, "timeoutMs": n}` | `ack`: ok when handled; ok false when the handler throws | 60 seconds |
| `call` | `{"kind": "intercept"\|"modify"\|"claim", "event": <event object>, "timeoutMs": n}` | `result`, with `value` as described below | 15 seconds |
| `action` | See below | `result`, with `value` `{"notice": "...", "url": "..."}` (url optional) | 30 seconds |
| `http` | See below | `result`, with `value` `{"status": 200, "headers": {"name": ["value"]}, "body": "<base64>"}` | 30 seconds |
| `schedule` | `{"scheduleId": "sweep", "cron": "*/10 * * * *", "at": "<due time>", "timeoutMs": n}` | `result`: ok once handled | 10 minutes |
| `shutdown` | None | No response; the host unloads every plugin and exits by itself; if it has not exited within 5 seconds, Go ends the process group | 5 seconds |

The `load` payload:

```json
{
  "id": "webhook", "version": "1.2.0",
  "root": "<install directory>", "entry": "<absolute path of the platform-side entry>", "dataDir": "<data directory, kept across versions>",
  "manifest": { "events": [], "permissions": [], "failClosed": false, "configSchema": {}, "accounts": [], "actions": [], "channels": [], "schedules": [], "http": false },
  "config": { "values": {}, "secrets": {}, "projects": { "<projectId>": { "values": {}, "secrets": {} } } },
  "token": "lnp_…", "apiUrl": "http://127.0.0.1:8780/api/v1"
}
```

- `config` holds the saved values as they are: one global copy, plus one per project that has the plugin enabled (only
  fields with `x-scope: project`; values and secrets are empty when the project has no project-level configuration). The
  keys of `projects` are exactly the projects that have the plugin enabled; projects that disabled it or were deleted
  are not included (even if configuration is left). The host builds `api.config.get(projectId)` by laying that
  project's values and secrets over the global ones. The payload of `config_changed` is `{"config": <as above>}`, always
  complete; it is sent when the global or a project configuration is saved and when a project enables or disables the
  plugin.
- `token` is valid only for this load: each load issues a new token and revokes the plugin's old one (the host reloads
  on restart and on a version update). The host calls `apiUrl` with it as `Authorization: Bearer` and may only call
  operations marked `x-plugin-scope` whose scope is in `permissions`.
- When loading, the host should check that every event the plugin code subscribes to is in `manifest.events`, and
  respond `ok: false` otherwise.

An event object = the fixed fields `id` (ULID), `type`, `at` (RFC 3339), `projectId`, `agentId`, `taskId` and `runId`
(null when absent), plus the fields of its type; when names collide, the fixed fields win.

The `action` payload: `{"actionId": "resend", "where": {"projectId"?, "agentId"?, "taskId"?, "identityId"?}, "input":
{...}, "timeoutMs": n}`; input has already been validated against the manifest's fields. When the response has ok
false, the console gets 502 plugin_failed with `error` as the explanation.

The `http` payload: `{"via": "external" | "console", "method": "POST", "path": "/callback", "query": "a=1", "headers":
{"content-type": ["application/json"]}, "body": "<base64>", "timeoutMs": n}`. `path` is what is left after removing
`/hooks/<id>` or `/api/v1/plugins/<id>/http` (it starts with `/`); header names are lowercase and `Authorization` and
`Cookie` are not included; the body is at most 1 MiB. The status, headers and body of the response are returned to the
caller as they are (hop-by-hop headers such as `Connection` and `Transfer-Encoding` are not allowed); when ok is false,
the caller gets 502.

`schedule`: Go fires it on the five-field cron of the manifest's `schedule[]` (in the server's local time); when the
previous run of the same entry has not responded yet, this one is skipped, and missed runs (host stopped, server down)
are not made up.

The `value` of `call`:

- `intercept`: `{"block": true, "reason": "..."}` blocks; anything else lets the operation through;
- `modify`: `{"patch": {...}}` is merged into the event field by field, and the next plugin sees the merged event;
- `claim`: `{"handled": true, ...}` claims the call and the whole object goes to the call site; anything else counts as
  not handled.

When the time limit is reached Go stops waiting and acts by kind (intercept lets the operation through, except that a
plugin whose manifest sets `failClosed` blocks it; modify is skipped; claim counts as not handled). The host should
trigger the call's `ctx.signal` when `timeoutMs` is reached; late responses are dropped.

## Host → Go

| type | Payload | Go response |
|---|---|---|
| `log` | `{"level": "debug"\|"info"\|"warn"\|"error", "message": "..."}` | None |
| `state_get` | `{"projectId": <uuid or null>, "key": "..."}` | `result`, with `value` the saved value, or null when there is none |
| `state_set` | `{"projectId", "key", "value": <any JSON>}` | `result` |
| `state_delete` | `{"projectId", "key"}` | `result` (succeeds when the key does not exist too) |
| `state_list` | `{"projectId", "prefix": "..."}` | `result`, with `value` `[{"key","value"}]` sorted by key, at most 1000 items |
| `accounts_list` | `{"projectId": "<uuid>", "kind": "lark"}` | `result`, with `value` `[{"id","kind","name","values"}]` in the order they were added, excluding deleted ones; `values` contains every field (secret fields included) |

`state_*` only work for a loaded plugin whose manifest declares the `state` permission; a null `projectId` means
plugin-global state; a key is 1 to 512 bytes and a value at most 1 MiB.

`accounts_list` only works for a plugin that is to be loaded (including during `load`, for example in its lifecycle
start) whose manifest declares the `accounts.read` permission; `kind` must be one of the manifest's `accounts[].kind`,
and the plugin must be enabled in `projectId`. Otherwise ok is false, and `error` is, in this order of checks:
`the plugin is not loaded`, `the manifest does not declare the accounts.read permission`, `the manifest does not
declare account kind <kind>`, `projectId is invalid`, `the plugin is not enabled in this project`.

## Delivering observation events

- Go delivers to each loaded plugin one event at a time: it takes the earliest due event (by event time) and marks it
  done only when the `ack` arrives. Different plugins are served in parallel.
- A failure (ack with ok false, a timeout, the host exiting) is redelivered after 5 seconds, 30 seconds, 2 minutes and
  10 minutes; if the 5th attempt still fails, the event is marked failed and recorded in the plugin log. While a
  redelivery is waiting, later events of the same plugin are delivered as usual.
- At least once: the host may receive an event twice (when it exits while handling and restarts); deduplicating by the
  event `id` is the plugin's job.

## Crashes and disabling

- After an unexpected exit the host is restarted after 1 second; after its 3rd exit within 5 minutes it is not
  restarted again, the plugins are marked as stopped with the host, and the exit reason, the last lines of standard
  error, the requests that were unanswered at exit and the most recent requests sent (plugin, event) are recorded. A
  manual "Restart host" resets the count.
- Disabling plugins globally: Go sends `shutdown` to make the host exit, which does not count as a crash; enabling them
  again starts the host and loads the plugins again.
