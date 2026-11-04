# Getting started

Run Loop Now as the **desktop app** (macOS Apple Silicon, nothing else to install) or from a **source installation**; the [README](https://github.com/AI-for-anyone/loop-now#getting-started) also covers Docker Compose. macOS Apple Silicon is the primary validated environment. Some components target Linux, but equivalent full-system coverage is not yet established; the Lark user CLI runtime is currently macOS arm64 only.

::: warning Early-stage personal edition
Loop Now targets a single local server. The console has no login and is intended for loopback access. Model and tool providers may still receive data when you use their services.
:::

## Desktop app (macOS Apple Silicon)

One DMG carries the server, the console, Node and Pi; nothing else is required.

1. Download `loop-now-desktop-<version>-mac-arm64.dmg` from [Releases](https://github.com/AI-for-anyone/loop-now/releases/latest), open it and drag Loop Now into Applications.
2. First launch: the package is not yet notarized by Apple, so macOS says it cannot verify the developer. Open System Settings › Privacy & Security, find the Security section and click "Open Anyway"; the prompt does not return.
3. A Loop Now icon appears in the menu bar and the window shows the console. Closing the window keeps the server and the agents running in the background; choose "Quit" from the menu bar icon to stop everything.

Where the data lives: the database and daily backups are in `~/Library/Application Support/Loop Now/default/data`, agent workspaces in `/Users/Shared/loop-now-desktop/default`, logs in `~/Library/Logs/Loop Now/default`. The desktop app never touches the source installation's `~/.loop_now` or `/Users/Shared/loop-now`, so both can coexist. To try it with a separate data set, start it with a profile: `open -a "Loop Now" --args --profile=trial`.

Updating: quit Loop Now, replace the app in Applications with the new DMG's copy and open it again; data is kept. "Check for updates" in the menu bar announces new versions and opens the download page; automatic installation arrives once the app is signed and notarized.

Agents in the desktop app run on the bundled Pi 1.4.0 and Node 24.21; model accounts, CLI accounts and plugins are configured in the console exactly as in a source installation. External identities and `loopctl` need the external TLS port, which the desktop app cannot open yet.

Continue with [Give your first agent a job](#give-your-first-agent-a-job).

## Source installation

### 1. Prepare dependencies

A source installation runs **one binary** (`loop-server`) on **Node.js 24** (Pi and the plugin host run on it) with **one data directory** (`~/.loop_now` by default). There is no separate database or cache to install. Building it needs:

| Dependency | Repository baseline |
| --- | --- |
| Go | 1.27 or later |
| Node.js / npm | **24.21.0 / 11.19.0**; Web scripts check these exact versions |
| Pi coding agent | **1.4.0** |
| Other tools | Git |

With the required Node and npm active:

```sh
npm install -g @earendil-works/pi-coding-agent@1.4.0
pi --version

git clone --depth 1 --branch main https://github.com/AI-for-anyone/loop-now.git
cd loop-now
```

### 2. Configure the server

Run the following from the repository root:

```sh
export LOOP_NOW_NODE_BIN="$(command -v node)"
export LOOP_NOW_PI_BIN="$(command -v pi)"
```

::: danger Plaintext credentials
The server keeps its database and daily backups in the data directory, `~/.loop_now` of the server user by default (`LOOP_NOW_DATA_DIR` changes it), and creates the database on first start. Credentials are stored in plaintext in that database; protect the data directory and its backups. The server reads process environment variables; it does not automatically load an `.env` file.
:::

Choose a persistent working directory **outside your home directory**, with no `AGENTS.md` or `CLAUDE.md` in its ancestor directories. This prevents Pi from inheriting unrelated host instructions. For a fresh installation, the default is `/Users/Shared/loop-now` on macOS or `/var/lib/loop-now` on Linux:

::: code-group
```sh [macOS]
export LOOP_NOW_WORK_ROOT='/Users/Shared/loop-now'
sudo mkdir -p "$LOOP_NOW_WORK_ROOT"
sudo chown "$(id -u):$(id -g)" "$LOOP_NOW_WORK_ROOT"
chmod 700 "$LOOP_NOW_WORK_ROOT"
```
```sh [Linux]
export LOOP_NOW_WORK_ROOT='/var/lib/loop-now'
sudo mkdir -p "$LOOP_NOW_WORK_ROOT"
sudo chown "$(id -u):$(id -g)" "$LOOP_NOW_WORK_ROOT"
chmod 700 "$LOOP_NOW_WORK_ROOT"
```
:::

### 3. Build and start the backend

Build the server and its matching `loopctl` distribution together:

```sh
mkdir -p bin
go build -o bin/loop-server ./cmd/loop-server
loop_version="$(bin/loop-server version)"
go run ./cmd/loopctl-dist -version "$loop_version" -out "$PWD/.local-build/loopctl-dist"

export LOOP_NOW_LOOPCTL_DIST_DIR="$PWD/.local-build/loopctl-dist"
bin/loop-server
```

Startup checks dependencies, applies pending database migrations, and creates the default organization and project. The API listens on `127.0.0.1:8780`. A data directory serves one server at a time; when updating, preserve the data directory and the working directory.

Plugins need the Node plugin host, built from `plugins/` (Node 24.21.0). The server runs it only when `LOOP_NOW_PLUGIN_HOST_ENTRY` is set, and offers the official plugins as the `builtin` install source when `LOOP_NOW_PLUGIN_OFFICIAL_DIR` is set:

```sh
(cd plugins && npm ci --ignore-scripts && npm run build)
export LOOP_NOW_PLUGIN_HOST_ENTRY="$PWD/plugins/host/dist/main.js"
export LOOP_NOW_PLUGIN_OFFICIAL_DIR="$PWD/plugins/official"
```

Set these before starting `bin/loop-server`. `dist/main.js` is a single self-contained file and can be copied elsewhere. Optional tool integrations may require their own dependencies and credentials.

#### Moving from a PostgreSQL-based release

Releases before the SQLite edition, up to v0.1.0, stored their data in PostgreSQL and Redis. To bring an installation over:

1. The import needs the database at the last PostgreSQL migration, which v0.1.0 has not reached. Check out `647fe00`, the last PostgreSQL-based commit, build it, start it once with your existing configuration so its migrations finish, then stop it.
2. With `LOOP_NOW_DATA_DIR` pointing at a data directory that has no database yet, run `loop-server import-postgres` with `LOOP_NOW_DATABASE_URL` set to the old database. It copies everything in one consistent snapshot, checks row counts and integrity, and prints each table's row count. On any failure it leaves nothing behind and never changes the old database.
3. Keep `LOOP_NOW_WORK_ROOT` as it was and start the new server on that data directory. Redis is no longer used.

### 4. Start the console

In a second terminal, from the repository root, with Node 24.21.0 and npm 11.19.0 active:

```sh
cd web
export LOOP_NOW_WEB_NODE_BIN="$(dirname "$(command -v node)")"
./bin/npm24 ci
./bin/npm24 run dev
```

Open **[http://localhost:5173](http://localhost:5173)**. The default API address is already configured. For a different address, see [`web/.env.example`](https://github.com/AI-for-anyone/loop-now/blob/main/web/.env.example).

## Give your first agent a job

1. Open a project and add a model account using a supported login or API key.
2. Create an Agent, select its model and account, and add instructions or Skills.
3. Add any repositories or files it needs. Enable plugins and grant tool access explicitly.
4. Start a conversation. For images, choose a model that supports image input.
5. When you want collaboration, add specialist agents and designate a project Manager.

Start with one agent and a small task before adding a longer workflow. Next, read [Concepts](/guide/concepts) to understand how organization, projects, agents, Inbox, and tasks fit together.
