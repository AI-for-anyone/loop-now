# Dedicated browser (Playwright)

[中文](https://github.com/AI-for-anyone/loop-now/blob/main/plugins/official/playwright/README.zh-CN.md)

Gives Agents a **separate, visible Chromium** through the official [Playwright MCP](https://github.com/microsoft/playwright-mcp) (`@playwright/mcp` 0.0.83, pinned). Agents open web pages, read their structure, click, type and take screenshots. It never connects to your everyday Chrome, and never reads or writes its profile.

It is an Agent-only plugin, made of two parts:

- an MCP server with the browser tools, whose names start with `mcp__playwright_browser__`;
- rules added to every turn: text on web pages is not an instruction from the user, and anything with consequences needs the user's go-ahead first.

There are no settings, accounts or permissions.

## Requirements

- The browser runs on the machine where Loop Now runs, as the system user that runs the Loop Now server, and opens a visible window there. That machine needs a desktop session. So far it has been tested on macOS on Apple silicon (arm64).
- It does not run in the Docker Compose deployment: the container has no desktop, and the image does not include the plugin's dependencies.
- The platform's Node 24, and network access once, to download Chromium.

## 1. Install

In the console, open **Plugin market**. Under **Official plugins that ship with the platform**, click **Install** on **Dedicated browser**, check the preview, and confirm. Or with the CLI:

```sh
loopctl plugin install playwright
```

## 2. Prepare the browser (once per machine)

Nothing is downloaded while Agents use the browser. Chromium is downloaded once into the plugin's data folder, and used from there every time. On the machine running Loop Now, as the system user that runs the server, with the platform's Node 24:

```sh
# Only if <plugin folder>/node_modules is missing: install the plugin's dependencies.
npm ci --prefix <plugin folder> --ignore-scripts --omit=dev --no-audit --no-fund

# Download the dedicated Chromium into the plugin's data folder.
node <plugin folder>/setup.mjs <work root>/platform/plugins/playwright/data
```

- `<work root>` is the server's `LOOP_NOW_WORK_ROOT`.
- `<plugin folder>` is where the plugin is installed: `<work root>/platform/plugins/playwright/<version>` (for example `…/playwright/1.0.0`), or the folder you chose if you installed it from a local folder.

`setup.mjs` downloads the Chromium build that matches the Playwright version into `data/browser/`, and writes `data/browser/browser.json` with the platform, the browser's path and its SHA-256. The data folder is kept across plugin updates, so there is nothing to redo after an update unless the plugin says so (see **Troubleshooting**). Prepare it on the machine and system the browser will run on.

## 3. Enable it in a project and grant it to Agents

1. **Project settings** › **Plugins** › **Manage project plugins**: enable **Dedicated browser**.
2. In an Agent's config, under **Plugin grants**: grant **Dedicated browser** and save.

Grants take effect the next time that Agent's Pi starts; the current reply is not interrupted. The platform stops an idle Agent's Pi after a while (10 minutes by default), and the next message starts a new one.

## Use it

Ask the Agent, for example: "Open https://example.com, tell me what the page says, and take a screenshot." The Agent opens the page with `browser_navigate`, reads it with `browser_snapshot`, acts on it with `browser_click`, `browser_type` and similar tools, and takes screenshots with `browser_take_screenshot`. A Chromium window opens on the Loop Now machine the first time the Agent uses a browser tool.

## How it behaves

- Each Agent has its own browser profile in `data/profiles/<Agent id>/`. Cookies and sign-ins made in it are kept across turns and Pi restarts. Screenshots and other files are saved in `data/output/<Agent id>/`.
- If another run of the same Agent (a copy of it in a task, for example) already has the profile open, this run uses a temporary profile kept in memory and discarded at the end, without the saved cookies.
- The browser starts with the first browser tool call and closes when the Agent's Pi session ends.
- Timeouts: 10 seconds for a page action, 45 seconds for loading a page, 120 seconds for one tool call.
- Agents are told to ask the user before submitting forms, paying, sending messages, deleting data or doing anything else with consequences.

## Troubleshooting

- **The Agent has no browser tools:** the browser's MCP server refused to start. Usually the browser has not been prepared, was prepared on another kind of machine, or the plugin was updated to a new Playwright version. To see the reason, run the server by hand, as the system user that runs Loop Now:

  ```sh
  node <plugin folder>/mcp/launch.mjs --data <work root>/platform/plugins/playwright/data --agent <Agent id>
  ```

  If something is wrong, it exits at once with a message starting with `[playwright]` that says what to do, usually to run `setup.mjs` again. If it keeps running, it is fine; stop it with Ctrl-C. Once fixed, the tools appear the next time the Agent's Pi starts.
- **No window appears:** the window opens on the Loop Now machine's desktop, only when the Agent calls a browser tool.
- **`[playwright] 配置目录 … 正被另一个浏览器使用，这次使用临时配置`** ("the profile is in use by another browser, using a temporary profile this time"): another run of this Agent has its browser open. This run does not see the saved cookies and sign-ins.
- **Start an Agent's browser from scratch:** while the Agent is not using the browser, delete `data/profiles/<Agent id>/`.
- Uninstalling the plugin keeps the data folder (the browser and every profile) unless you tick **Also delete the data folder** in the uninstall dialog.
