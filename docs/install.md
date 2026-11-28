# Install Loop Now — instructions for an AI agent

You are an AI coding agent (Claude Code, Codex, Cursor, …) asked to install Loop Now on the computer you run on.
Follow these steps in order. Work in the person's terminal, show each command before you run it, and stop to ask the
person whenever a step below says **ask**.

Loop Now is a local multi-agent workspace: one server on this computer runs Agents (on its bundled Pi or on CLIs the
person already uses, such as Claude Code or Codex) and serves a web console. Repository:
https://github.com/AI-for-anyone/loop-now. Releases: https://github.com/AI-for-anyone/loop-now/releases/latest.

## Rules

- Never print, copy or move credentials, tokens or login files. Loop Now never needs them from you.
- Never delete or overwrite existing Loop Now data: `~/Library/Application Support/Loop Now`, `~/.loop_now`,
  `/Users/Shared/loop-now`, `/Users/Shared/loop-now-desktop`, `/var/lib/loop-now`. **Ask** before replacing an installed
  copy of the app.
- Do not bypass macOS Gatekeeper or other security prompts yourself; tell the person what to click.
- Do not use `sudo` unless a step says so, and **ask** before you do.
- At the end, report what you installed, where, the console address, and anything left for the person.

## 1. Find out what this computer is

```sh
uname -s   # Darwin = macOS, Linux = Linux
uname -m   # arm64/aarch64 or x86_64
git --version
```

- Git is required. On macOS without it, `git --version` opens the Command Line Tools installer: **ask** the person to
  finish it, then continue. On Linux, install `git` with the system package manager (**ask** first).
- Windows is not supported yet. Stop and tell the person.

Then pick the path:

| Computer | Path |
| --- | --- |
| macOS on Apple silicon (`Darwin` + `arm64`) | **A. Desktop app** (recommended) |
| macOS on Intel, or Linux (x86_64 / arm64) | **B. Server bundle** |

If the person asked for a specific path (for example the server bundle on an Apple silicon Mac), follow that instead.

## 2. Find the latest release

```sh
curl -fsSL https://api.github.com/repos/AI-for-anyone/loop-now/releases/latest \
  | grep -E '"(tag_name|browser_download_url)"'
```

Note the version (`tag_name`, for example `v0.3.7`; the files use it without the `v`). Every release has `SHA256SUMS`.

## A. Desktop app (macOS, Apple silicon)

1. If `/Applications/Loop Now.app` already exists, **ask** whether to replace it (data is kept either way). If Loop Now is
   running, ask the person to quit it from the menu bar icon first.
2. Download and check:

   ```sh
   V=0.3.7   # the version from step 2, without the v
   mkdir -p /tmp/loop-now-install && cd /tmp/loop-now-install
   curl -fLO "https://github.com/AI-for-anyone/loop-now/releases/download/v$V/loop-now-desktop-$V-mac-arm64.dmg"
   curl -fLO "https://github.com/AI-for-anyone/loop-now/releases/download/v$V/SHA256SUMS"
   shasum -a 256 -c --ignore-missing SHA256SUMS
   ```

   The check must print `OK` for the DMG. If it does not, stop and tell the person.
3. Install:

   ```sh
   hdiutil attach -nobrowse "loop-now-desktop-$V-mac-arm64.dmg"
   cp -R "/Volumes/Loop Now/Loop Now.app" /Applications/   # the volume name may differ; use the one hdiutil printed
   hdiutil detach "/Volumes/Loop Now"
   open -a "Loop Now"
   ```

4. The app is not notarized by Apple yet, so macOS refuses the first launch. Tell the person: open **System Settings ›
   Privacy & Security**, find Loop Now in the Security section, click **Open Anyway**, confirm. It does not ask again.
5. The console opens in the Loop Now window and a Loop Now icon appears in the menu bar. On first launch Loop Now offers to
   install the `loopctl` command line tool (it asks for the macOS password); tell the person they can accept or skip it.
6. Data lives in `~/Library/Application Support/Loop Now/default` (database), `/Users/Shared/loop-now-desktop/default`
   (Agent folders) and `~/Library/Logs/Loop Now/default` (logs).

## B. Server bundle (macOS Intel, Linux)

1. Download and check (`PLATFORM` is `darwin-amd64`, `darwin-arm64`, `linux-amd64` or `linux-arm64`):

   ```sh
   V=0.3.7; PLATFORM=linux-amd64
   mkdir -p ~/loop-now && cd ~/loop-now
   curl -fLO "https://github.com/AI-for-anyone/loop-now/releases/download/v$V/loop-now-$V-$PLATFORM.tar.gz"
   curl -fLO "https://github.com/AI-for-anyone/loop-now/releases/download/v$V/SHA256SUMS"
   sha256sum -c --ignore-missing SHA256SUMS      # macOS: shasum -a 256 -c --ignore-missing SHA256SUMS
   tar -xzf "loop-now-$V-$PLATFORM.tar.gz"       # use tar, not Finder: Finder marks the files as quarantined
   ```

2. Choose the work root (where Agents keep their folders). It must be outside the home directory and have no `AGENTS.md` or
   `CLAUDE.md` above it. Default: `/var/lib/loop-now` on Linux, `/Users/Shared/loop-now` on macOS. **Ask** before creating
   it with `sudo`:

   ```sh
   export LOOP_NOW_WORK_ROOT=/var/lib/loop-now   # macOS: /Users/Shared/loop-now
   sudo mkdir -p "$LOOP_NOW_WORK_ROOT"
   sudo chown "$(id -u):$(id -g)" "$LOOP_NOW_WORK_ROOT"
   chmod 700 "$LOOP_NOW_WORK_ROOT"
   export LOOP_NOW_DATA_DIR="$HOME/.loop_now"
   ```

3. Start it. **Ask** whether the person wants it in the foreground, in the background (`nohup … &`), or as a service:

   ```sh
   cd "loop-now-$V-$PLATFORM"
   ./loop-server
   ```

4. Check it and open the console:

   ```sh
   curl -fsS http://127.0.0.1:8780/api/v1/health
   ```

   Then open http://127.0.0.1:8780/ in the browser. The bundle carries its own Node and Pi; nothing else is needed.

## 3. After it runs

Tell the person these next steps; do them only if asked:

- **Agents on CLIs they already use.** Loop Now can run an Agent on Claude Code, Codex, Antigravity or Grok installed on this
  computer, with the person's own login. **Global settings › Agent runtimes** shows which ones it found. A CLI must be
  installed and signed in by the person in their own terminal first.
- **Agents on the bundled Pi** need a model account: add one in the project (API key or supported sign-in).
- **First Agent:** create a project, add an Agent, pick who runs it (Pi or a CLI), give it a folder, and send a message.
- **Phone access (optional):** **Settings › Phones and other devices** — sign in to a Loop Now account, turn on access,
  and scan the pairing QR code with the phone.
- **Import existing conversations (optional):** Claude Code and Codex conversations can be imported from the console.

## 4. Report

Finish with a short summary for the person: the version installed, the path you used (A or B), where the app or bundle is,
the console address, whether `loopctl` was installed, and anything they still need to click or decide.
