# Getting started

The current setup is a **source installation**. macOS Apple Silicon is the primary validated environment. Some components target Linux, but equivalent full-system coverage is not yet established; the Lark user CLI runtime is currently macOS arm64 only.

::: warning Early-stage personal edition
Loop Now targets a single local server. The console has no login and is intended for loopback access. Model and tool providers may still receive data when you use their services.
:::

## 1. Prepare dependencies

| Dependency | Repository baseline |
| --- | --- |
| Go | 1.27 or later |
| Node.js / npm | **24.21.0 / 11.19.0**; Web scripts check these exact versions |
| PostgreSQL | 18 |
| Redis | 8 |
| Pi coding agent | **1.4.0** |
| Other tools | Git |

With the required Node and npm active:

```sh
npm install -g @earendil-works/pi-coding-agent@1.4.0
pi --version

git clone --depth 1 --branch main https://github.com/AI-for-anyone/loop-now.git
cd loop-now
```

Start PostgreSQL and Redis using your preferred local installation. Create a dedicated PostgreSQL role and database; run these as your local PostgreSQL administrator:

```sh
createuser --login --pwprompt loopnow
createdb --owner=loopnow loopnow
```

## 2. Configure the server

Run the following from the repository root. Replace the database password with the one you chose, URL-encoded if necessary. Use a dedicated Redis database and key prefix; add Redis authentication to the URL if your server requires it.

```sh
export LOOP_NOW_DATABASE_URL='postgres://loopnow:YOUR_URL_ENCODED_PASSWORD@127.0.0.1:5432/loopnow?sslmode=disable'
export LOOP_NOW_REDIS_URL='redis://127.0.0.1:6379/1'
export LOOP_NOW_REDIS_KEY_PREFIX='loopnow:'
export LOOP_NOW_NODE_BIN="$(command -v node)"
export LOOP_NOW_PI_BIN="$(command -v pi)"
```

::: danger Plaintext credentials
Credentials are stored in plaintext in PostgreSQL; protect database access and backup files. The server reads process environment variables; it does not automatically load an `.env` file.
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

## 3. Build and start the backend

Build the server and its matching `loopctl` distribution together:

```sh
mkdir -p bin
go build -o bin/loop-server ./cmd/loop-server
loop_version="$(bin/loop-server version)"
go run ./cmd/loopctl-dist -version "$loop_version" -out "$PWD/.local-build/loopctl-dist"

export LOOP_NOW_LOOPCTL_DIST_DIR="$PWD/.local-build/loopctl-dist"
bin/loop-server
```

Startup checks dependencies, applies pending database migrations, and creates the default organization and project. The API listens on `127.0.0.1:8780`. Run one server per installation; when updating, preserve the database, Redis configuration, and working directory.

Plugins need the Node plugin host, built from `plugins/` (Node 24.21.0). The server runs it only when `LOOP_NOW_PLUGIN_HOST_ENTRY` is set, and offers the official plugins as the `builtin` install source when `LOOP_NOW_PLUGIN_OFFICIAL_DIR` is set:

```sh
(cd plugins && npm ci --ignore-scripts && npm run build)
export LOOP_NOW_PLUGIN_HOST_ENTRY="$PWD/plugins/host/dist/main.js"
export LOOP_NOW_PLUGIN_OFFICIAL_DIR="$PWD/plugins/official"
```

Set these before starting `bin/loop-server`. `dist/main.js` is a single self-contained file and can be copied elsewhere. Optional tool integrations may require their own dependencies and credentials.

## 4. Start the console

In a second terminal, from the repository root, with Node 24.21.0 and npm 11.19.0 active:

```sh
cd web
export LOOP_NOW_WEB_NODE_BIN="$(dirname "$(command -v node)")"
./bin/npm24 ci
./bin/npm24 run dev
```

Open **[http://localhost:5173](http://localhost:5173)**. The default API address is already configured. For a different address, see [`web/.env.example`](https://github.com/AI-for-anyone/loop-now/blob/main/web/.env.example).

## 5. Give your first agent a job

1. Open a project and add a model account using a supported login or API key.
2. Create an Agent, select its model and account, and add instructions or Skills.
3. Add any repositories or files it needs. Enable plugins and grant tool access explicitly.
4. Start a conversation. For images, choose a model that supports image input.
5. When you want collaboration, add specialist agents and designate a project Manager.

Start with one agent and a small task before adding a longer workflow. Next, read [Concepts](/guide/concepts) to understand how organization, projects, agents, Inbox, and tasks fit together.
