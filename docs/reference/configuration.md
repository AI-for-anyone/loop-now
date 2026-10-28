# Configuration

All server options and defaults are defined in [`internal/server/config/config.go`](https://github.com/AI-for-anyone/loop-now/blob/main/internal/server/config/config.go). The server reads process environment variables; it does not automatically load an `.env` file.

## Frequently adjusted settings

| Variable | Purpose / default |
| --- | --- |
| `LOOP_NOW_LISTEN_ADDR` | Console API; `127.0.0.1:8780` |
| `LOOP_NOW_WORK_ROOT` | Persistent agent workspaces and runtime files |
| `LOOP_NOW_CONTEXT_DIR` | Shared project (`projects/<id>/`) and global (`global/`) context folders; `~/.loop_now` of the server user |
| `LOOP_NOW_PLUGIN_HOST_ENTRY` | Absolute path of the plugin host entry run with Node; unset means plugin platform halves do not run |
| `LOOP_NOW_PLUGIN_OFFICIAL_DIR` | Absolute path of the official plugins shipped with the platform (one directory per plugin); unset means no `builtin` install source |
| `LOOP_NOW_PLUGIN_MARKET_URL` | `https` address of the official plugin market's `loop-now-market.json`; defaults to the official market index and can point elsewhere |
| `LOOP_NOW_PI_MAX_PROCESSES` | `0` means unlimited; a positive value caps local Pi processes |
| `LOOP_NOW_PI_IDLE_TIMEOUT` | Idle Pi process timeout; `10m` |
| `LOOP_NOW_CONSOLE_ORIGINS` | Allowed browser origins; local port 5173 by default |
| `LOOP_NOW_LARK_USER_BUNDLE_DIR` | Optional verified Lark user CLI bundle |

## Required for a source installation

| Variable | Purpose |
| --- | --- |
| `LOOP_NOW_DATABASE_URL` | PostgreSQL connection string; credentials are stored in plaintext in this database |
| `LOOP_NOW_REDIS_URL` | Redis connection string; use a dedicated database and `LOOP_NOW_REDIS_KEY_PREFIX` |
| `LOOP_NOW_NODE_BIN` | Path of the Node binary used to run the plugin host |
| `LOOP_NOW_PI_BIN` | Path of the Pi coding agent binary |
| `LOOP_NOW_LOOPCTL_DIST_DIR` | Directory holding the matching-version `loopctl` distribution |

## Security boundaries

The current release is intended for **trusted personal use**:

- The console has no authentication. Keep it on loopback rather than exposing it to a public network.
- Agents execute local commands under the server's operating-system user. A separate task directory is not an OS security sandbox.
- Plugins execute code; install sources you trust. Model and tool access also depends on the credentials and permissions you provide.
- Conversation history is persistent, but a server restart is not a promise of uninterrupted execution or automatic replay of in-flight work. Prefer updating when agents are idle.
