# Configuration

All server options and defaults are defined in [`internal/server/config/config.go`](https://github.com/AI-for-anyone/loop-now/blob/main/internal/server/config/config.go). The server reads process environment variables; it does not automatically load an `.env` file.

## Frequently adjusted settings

| Variable | Purpose / default |
| --- | --- |
| `LOOP_NOW_LISTEN_ADDR` | Console API; `127.0.0.1:8780` |
| `LOOP_NOW_DATA_DIR` | Database (`db/`) and daily backups (`backups/`); `~/.loop_now` of the server user |
| `LOOP_NOW_WORK_ROOT` | Persistent agent workspaces and runtime files; like the data directory, one server instance at a time (locked by `.loop-now-server.lock`) |
| `LOOP_NOW_CONTEXT_DIR` | Shared project (`projects/<id>/`) and global (`global/`) context folders; `~/.loop_now` of the server user |
| `LOOP_NOW_PLUGIN_HOST_ENTRY` | Absolute path of the plugin host entry run with Node; unset means plugins' platform sides do not run |
| `LOOP_NOW_PLUGIN_OFFICIAL_DIR` | Absolute path of the official plugins shipped with the platform (one directory per plugin); unset means no `builtin` install source |
| `LOOP_NOW_PLUGIN_MARKET_URL` | `https` address of the official plugin market's `loop-now-market.json`; defaults to the official market index and can point elsewhere |
| `LOOP_NOW_PI_MAX_PROCESSES` | `0` means unlimited; a positive value caps local Pi processes |
| `LOOP_NOW_PI_IDLE_TIMEOUT` | Idle Pi process timeout; `10m` |
| `LOOP_NOW_CONSOLE_ORIGINS` | Cross-origin browser origins allowed to call the API, for the Vite dev server; local port 5173 by default, `none` allows none. The console the server serves itself is same-origin and needs no entry |
| `LOOP_NOW_LARK_USER_BUNDLE_DIR` | Optional verified Lark user CLI bundle |

## Data directory and backups

The database is SQLite: `data.db` holds platform state and credentials (in plaintext), `events.db` holds run and audit logs. Only one server may use a data directory at a time. Every day the server checkpoints the database, removes run and audit logs older than 90 days, and writes a backup.

| Variable | Purpose / default |
| --- | --- |
| `LOOP_NOW_DATA_DIR` | Data directory; `~/.loop_now` of the server user |
| `LOOP_NOW_BACKUP_DIR` | Where daily backups go, one dated subdirectory each; `<data directory>/backups` |
| `LOOP_NOW_BACKUP_KEEP` | How many daily backups to keep; `7` |

`loop-server backup <directory>` writes a backup on demand, also while the server runs. `loop-server import-postgres` moves an installation from a PostgreSQL-based release once (see [Getting started](/guide/getting-started#moving-from-a-postgresql-based-release)).

## Required for a source installation

| Variable | Purpose |
| --- | --- |
| `LOOP_NOW_NODE_BIN` | Path of the Node binary used to run the plugin host |
| `LOOP_NOW_PI_BIN` | Path of the Pi coding agent binary |
| `LOOP_NOW_LOOPCTL_DIST_DIR` | Directory holding the matching-version `loopctl` distribution (a release bundle uses the one next to `loop-server`) |

## Release bundle and desktop shell

A release bundle (`scripts/release/build.sh`, or the `release` workflow) unpacks to one directory:

| Path | Contents |
| --- | --- |
| `loop-server` | The server, with the console built in and served on its port at `/` |
| `loopctl-dist/` | `loopctl` for every platform |
| `plugins/host/main.js`, `plugins/official/` | Plugin host and official plugins |
| `runtime/node/` | Node 24.21.0 with npm |
| `runtime/pi/` | Pi coding agent 1.4.0 installed with that Node |

When `LOOP_NOW_NODE_BIN`, `LOOP_NOW_PI_BIN`, `LOOP_NOW_LOOPCTL_DIST_DIR`, `LOOP_NOW_PLUGIN_HOST_ENTRY` or `LOOP_NOW_PLUGIN_OFFICIAL_DIR` is unset and the matching file sits next to `loop-server`, the server uses it, so the machine needs no Node or Pi of its own. The configured Node's directory comes first on the agents' `PATH`.

| Variable | Purpose |
| --- | --- |
| `LOOP_NOW_WEB_DIR` | Serve the console from this directory instead of the built-in one |
| `LOOP_NOW_READY_FILE` | Once serving, write `{"url","pid","version"}` to this file (removed on shutdown); use it with `LOOP_NOW_LISTEN_ADDR=127.0.0.1:0` to learn the port. Readiness: `GET <url>/healthz` |

Exit codes of `loop-server`: `0` normal shutdown, `1` other startup failure or an error while shutting down, `2` invalid configuration, `3` the data directory or the work root is used by another instance, `4` a listen address is already in use.

## Security boundaries

The current release is intended for **trusted personal use**:

- The console has no authentication. Keep it on loopback rather than exposing it to a public network.
- Agents execute local commands under the server's operating-system user. A separate task directory is not an OS security sandbox.
- Plugins execute code; install sources you trust. Model and tool access also depends on the credentials and permissions you provide.
- Conversation history is persistent, but a server restart is not a promise of uninterrupted execution or automatic replay of in-flight work. Prefer updating when agents are idle.
