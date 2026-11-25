# Changelog

中文：[CHANGELOG.zh-CN.md](CHANGELOG.zh-CN.md)

User-facing changes in each version. The release pipeline takes the matching version's section as the GitHub Release notes (see `scripts/release/notes.sh`), followed by the Chinese section from CHANGELOG.zh-CN.md.

## Unreleased

### Write with AI

- Every Prompt has a "Write with AI" button: the Prompt of a new Agent, an Agent's Prompt on its config page, and a project's Prompt. Say in a line what the Agent (or the project) should do; the draft replaces the text in the box, Undo brings the old text back, and nothing is saved until you save as before. With text already in the box, the request may be left empty to have it improved.
- Settings has a new section, "Writing with AI", that says who writes. Automatic, the default, takes the first CLI on this computer that is ready (Claude Code, Codex, Antigravity, Grok, then your own ACP commands) and a small, quick model of it. You can choose one instead; when it is not ready, the draft says who wrote it instead.
- Each draft runs that CLI once with its own login on this computer, in an empty temporary folder, without tools, plugins or MCP servers, and leaves nothing in the CLI's history. No Agent is created. The bundled Pi does not write: its keys belong to a project or an Agent.
- API: `GET`/`PUT /text-generation-settings` and `POST /prompt-drafts`; new error codes `text_generation_unavailable` and `text_generation_failed`.

## v0.3.0

Antigravity works as it should: commands that printed output no longer stay "in progress" (the same fix covers Grok and local ACP commands), a reply waits for the commands Antigravity runs in the background and shows what it says about them afterwards, and its models have thinking levels. In the desktop app, path fields have a "Choose…" button that opens the system's dialog.

### Antigravity: finished commands show as finished, and its background commands are waited for

- A tool that ended with output, such as any command that printed something, stayed "in progress" forever: its end message carries its output as a list, and the whole message was dropped. This affected Antigravity, Grok and local ACP commands. The end is now recorded.
- Antigravity moves a command that runs longer than its wait to the background and ends its reply; when the command finishes, it reports the result by itself. The reply now stays open until those commands have finished and Antigravity's own follow-up has come, and the follow-up appears in the same reply. If nothing comes from Antigravity for 30 minutes (`LOOP_NOW_ACP_BACKGROUND_WAIT`), the reply ends there and the run record says why. Stop ends it at once and stops the background commands; messages sent meanwhile are queued as usual.
- A tool that never reported how it ended shows "No result" once the reply is over, instead of "in progress". Older records show the same.
- API: `ToolState` gains `no_result`; new run record entry `background_wait_timeout`.
- Upgrade: the events database's tool table is rebuilt once (about 5 seconds for 2.5 million rows; the file keeps the freed space for later writes).
- Downgrade: older versions show `no_result` tools without a state in the chat and as still running in task records, and do not show the new run record entry.

### Antigravity models have thinking levels

- Antigravity lists every thinking level as a model of its own (Gemini 4 Neon (Low), Gemini 4 Neon (Medium) and so on). These are now one model with its levels to choose, as for the other runtimes; the default is medium where it is listed. Models without levels, such as Orchestrator, are unchanged.
- Agents saved with a per-level model keep it: when Antigravity's models are first read, they move to the model and that level. Task instances keep the model they started with.

### Choose a folder or a program in the desktop app

- In the desktop app, path fields have a "Choose…" button that opens the system's dialog: an Agent's or a project's local folders, a plugin installed from a local folder, and the command of a local ACP command. Choosing an `.app` fills in the program inside it, and hidden folders such as `~/.local/bin` are shown. The path can still be typed.
- In a browser the fields are unchanged: a web page cannot learn a folder's full path.

## v0.2.7

Besides Pi and Claude Code, Agents can now run on Codex, Grok, Antigravity or any ACP command you add, all with your own login on this computer; you pick who runs an Agent from the message box before any message, and the conversation carries over. All console tabs share one live connection, so several open tabs no longer freeze the browser. The console redesign reaches task details, plugin details and dialogs; platform Agents can use model providers from plugins; logs and error messages are in English.

### Pick who runs an Agent, and change it at any message

- Creating an Agent now starts with "Who runs it": the CLIs found on this computer come first, with their state (ready, not installed, not logged in), and the bundled Pi comes last with its model providers, those you can use now on top. A runtime that is not ready shows the command to install it or log in. Models are searchable, across all of Pi's providers too.
- The same picker is on the Agent's config page and in the message box. The message box shows who runs the Agent now; choosing there saves at once and applies from the next message, also while the Agent is replying. The choice stays until you change it, and messages nobody types (from other Agents, wake-ups) use it too.
- Changing the runtime no longer starts the conversation over. The new runtime gets the saved conversation with your next message: all of it when it is short, otherwise your first request, the most recent messages and a one-line summary of tool use per turn, up to a budget (16,000 tokens by default, `LOOP_NOW_CONTEXT_HANDOFF_TOKENS`). Reasoning, tool state and images are not carried. The conversation shows where it happened and how many messages went along; a change of model alone shows a small divider.
- Changing the runtime and straight back, before a message reached the new one, simply continues the earlier session. `/new` still starts clean.
- Pending sleeps are no longer cancelled by a change of runtime; the wake-up goes to the new one.
- An Agent can read its own saved conversation with `loopctl conversation`.
- API: `Run.selection` and `Run.contextHandoff`, a `context_handoff` timeline entry, `usable` and `?include=models` on `GET /model-providers`, `setup` on runtime definitions, `GET /conversation` in the Agent API. Changing the runtime during a reply no longer returns 409.
- Downgrading: an older version runs on the new database but does not know a handoff that is still pending; it starts the conversation over on a change of runtime, as before.

### Agents can run on Codex

- A new runtime: the Codex CLI installed on your computer, with your own `codex login`. The platform neither reads nor stores the login. It appears in "Who runs it" as Codex, with the models your account can use (read from Codex itself, again every 10 minutes and when you choose "Check again").
- It needs Codex 0.167.0 or later; an older one shows "Too old" with the command to update.
- The Agent's conversations are kept by Codex in your own Codex history (`~/.codex`); deleting the Agent archives them there, it does not delete them.
- Codex loads your own Codex settings (your MCP servers, your skills in `~/.codex`) together with your login. The Agent runs with its own home directory, so `gh`, `git` and other commands do not use your logins unless "Use local logins" is on.
- It runs commands without asking first, like the other runtimes. Skills, plugin MCP tools, `/compact`, tasks and changing runtime in the middle of a conversation work. Not offered yet: the `ultra` thinking level, the fast service tier, `/export` and `/resume`.
- When Codex says it is logged in but the login was revoked, the status still shows "logged in"; the first message then fails and says to run `codex login`.
- Console: errors name the runtime that failed instead of always saying Claude Code; "session file missing" is now "session not found"; each local CLI has its own mark in the sidebar.
- API: `AgentRuntime` gains `codex`, the provider is `codex-cli`; `minVersion` on runtime definitions; `version_unsupported` as a reason a runtime is unavailable.

### Agents can run on Grok, Antigravity, and any ACP command you add

- Two more runtimes that use CLIs installed on your computer and your own logins there: Grok (`grok agent stdio`) and Antigravity (`agy acp`). Both speak the Agent Client Protocol (ACP). The platform neither reads nor stores their logins. They appear in "Who runs it" with the models each CLI lists.
- You can add any other program that speaks ACP in Settings → Agent runtimes → "ACP commands you added": a name, the command, its arguments and environment variables. It runs on this computer as the server's user, so add only programs you trust. Saved variable values are never shown again. A command cannot be deleted while Agents use it; the page lists them.
- Limits of these runtimes: they report no token usage (a turn says "no usage data"); their conversations stay in the CLI's own history and are not removed when an Agent is deleted, because the protocol has no way to do it; `/compact`, `/export` and `/resume` are not available; tasks start from a selection of the regular conversation instead of a copy of the session. Antigravity's model names include the thinking level, so there is none to choose. A command you add has one model, "Default model", and takes no images.
- When a session can no longer be found (its file was deleted, or the CLI lost it), the next message now starts a new session with the saved conversation, and the conversation shows a line saying so. Before, the Agent stopped until you confirmed starting over. This applies to Pi, Claude Code and Codex as well; the red "session lost" notice and its confirmation are gone.
- A turn records which runtime it was meant for from the moment it is sent, so a turn that fails before starting names the right one; the overview's problem list and task turns name it too.
- A process that exits because you stopped a turn is no longer recorded as an unexpected exit.
- The message box does not accept images for a model that takes none.
- API: `AgentRuntime` is now a plain string (`grok`, `antigravity`, and `acp-<id>` for a command you added); `/acp-commands`; `Run.usageReported`; a `reason` on handoffs (`runtime_changed`, `session_lost`, `task_start`); `runtime` on problems and task messages; error code `runtime_in_use`.

### Several console tabs no longer freeze the browser

- A browser allows six connections to one address, and every console tab kept two open for live updates. With three tabs open nothing else could load, not even a reload. All tabs of one browser now share their live connections through one shared worker, so opening more tabs of the same pages costs no more connections.
- All live updates of all tabs now travel over one connection, whatever the tabs show: the inbox, the overview, any number of Agents and tasks.
- Nothing changes in what a tab shows. Where the browser has no shared workers, each tab uses one connection of its own.
- API: `GET /events` carries any set of the existing event streams on one connection; the five separate stream endpoints stay.

### Console redesign (step 5)

- The task details move onto the sheet, and the task status becomes a stamp (red when blocked). The flow diagram is no longer a canvas you can drag and zoom; it is now a vertical ruler in the stub on the right of the sheet: one cell per node, a yellow dot on the node the task has reached, and a marker and arrow that follow the node you are viewing; a rework loop shows how many times it has been used (for example "used 2/3"). In narrow windows the flow ruler is replaced by a row of step labels.
- Task conversation: multiple visits to the same node are separated by cut lines, and from a cut line you can open the node materials for that visit; the node materials show the visit you are viewing first, with the others collapsed. The input area matches the Agent chat page (pill-shaped input box, pink round send button). "Switch Lark account and continue QA" is now a button next to "Retry current node" and expands only when clicked; it expands automatically when an account switch is in progress or has failed.
- Plugin details: the overview is split into numbered sections (What it contains, Enabled in projects, Recent activity, About); events, deliveries and logs are combined in the overview into the three numbers of "Recent activity" (counting only what has been loaded, with a "+" when not everything is loaded); the stub on the right holds the contents ruler, the current version and the update status. The Config, Accounts, Events, Deliveries and Logs tabs move onto the same sheet, with their content unchanged.
- All dialogs now share one form, a small sheet: an index tab on the top edge names the category (yellow for creating something, red for dangerous actions, blue for everything else), a yellow round close button sits in the top right corner, a dashed line runs above the button area, and the primary button is pink.
- Confirmation dialogs for dangerous actions have a red shadow and a red confirm button; actions that delete something for good carry an "Irreversible" stamp, while those that can be added back (removing a local directory, removing a CLI that has no credentials entered) do not.
- Issue-token dialog: the token sits in a dashed box, the copy button is yellow, and the reminder that the token is shown only this once is a yellow sticky note.
- Drawers (creating and viewing details in the Skills library, model examples): the header has halftone dots, and the button bar at the bottom is light yellow.

### Platform Agents can also use model providers from plugins

- Agents that do not belong to any project can now also use model providers that come from plugins (such as pi-grok-cli). Grant the plugin to the Agent under "Plugin grants" in the Agent config, and its provider can be chosen as the model provider; without the grant, the picker explains why. It still cannot be chosen directly when creating an Agent; create the Agent, grant the plugin, then change it.
- The subscription login belongs to the Agent: log in, log in again or cancel under this Agent's "Model account credential", with the same process as logging in within a project (browser authorization, device code, and answering on the page when the extension asks you to choose or paste an authorization code). Each platform Agent logs in once on its own and does not share project accounts.
- The other rules are the same as for project Agents: the login state is saved and refreshed by the extension itself and is not stored in the database; with "Use local logins" on, the extension's own login on this computer is used, and the page tells you whether this computer has that login; uninstalling the plugin keeps the login by default and clears it as well when "Also delete the data folder" is checked.
- API: `POST /agents/{agentId}/model-auth/operations` accepts plugin providers; `ModelAuthOperation` gains `deviceCode` and `prompt`; new `POST /agents/{agentId}/model-auth/operations/{operationId}/input`; `ModelAuthState` gains an optional `localLogin`; `platform_agent` is removed from the unavailability reasons of `GET /model-providers`.

### Logs are now in English

- Server logs, the desktop app's `shell.log` and the plugin host's logs are now in English, so that contributors who do not read Chinese can troubleshoot. Log field names and UI copy are unchanged.
- All comments in the code are now in English (no effect on use).
- The descriptions in the API definitions (`api/openapi.yaml`, `api/agent-openapi.yaml`) are now in English; paths, fields, enums and validation rules are all unchanged.

### Error messages are now in English

- Error messages returned by the server, loopctl and the other command-line programs are now in English, for users and contributors who do not read Chinese. Error codes, HTTP statuses and field names are all unchanged.
- The Chinese UI still shows a Chinese explanation for each error code; details that carry specifics (such as which field, by how much a limit was exceeded, or why a run failed) are appended in the original English. The English UI now shows these details too. Validation hints next to form inputs are in English for now.
- Places that only turn an error code into a sentence (the failure explanation of a task turn, model login, Lark switch receipts) show only the UI's own copy and no longer append the server's sentence as well.
- In the inbox, "Platform", "Console", "Deleted Agent" and "Deleted identity" are now shown by the UI in its own language.
- Added a batch of status explanations that used to appear as raw English codes or not at all: why a task message was not run, why a Lark login failed and why a participant is unavailable, why a connection test is not supported, credential template statuses, and so on.
- Fix: the `cli_auth_in_progress` and `credential_selection_mode_conflict` errors used to be treated as unknown errors, showing the server's original text instead of the UI's copy.
- Chinese explanations saved in existing data are unchanged and are shown as they are.
- API: when a request's method and path match no endpoint, the 404 `not_found` carries `details.reason = "unknown_endpoint"` (new `NotFoundDetails`); it is absent when a resource does not exist.
- Desktop app: the messages for an invalid configuration file and for a failed loopctl installation follow the system language.
- The plugin host's and SDK's errors, validation results and logs are now in English; `api/plugin-host-protocol.md` is now in English.
- Prompt given to Agents: with "Use local logins" on, the text for a platform Agent no longer speaks of "the subscription account configured in the project".

### Labels follow the UI language

- The field labels of the CLI credential templates (gh, gcloud, mysql, psql, lark-cli) and the form text of the official plugins (hello, webhook, playwright) now come in English and Chinese: the English UI shows English and the Chinese UI shows Chinese. Until now they were Chinese only.
- Plugin manifests can give a `LocalizedText` (a string, or an object keyed by `en-US` and `zh-CN`) for channel, action and account-kind labels, and for `title` and `description` at any level of `configSchema` and of account and action fields, including the titles of `oneOf` options. A plain string is treated as English and shown in every language, so existing manifests keep working.
- API: `CliTemplateField.label`, `PluginAccountKind.label`, `PluginAction.label` and `PluginComponents.channels[].label` are now `LocalizedText`.
- An official plugin that is already installed keeps the labels stored at installation until it is reinstalled or updated.
- An account kind with `"label": null` is now rejected, like a null `displayName`.
- Downgrading: once a manifest or template with two-language labels is stored, an older version cannot read it.

### Fixes

- A platform Agent that had a model key and was switched to Claude Code failed every run with "credentials unavailable", and the key could not be removed from its page. An Agent on Claude Code now ignores the model key; the key stays stored and is used again if the Agent is switched back to its provider.

### Groundwork for more Agent runtimes

- Nothing changes in use: Agents on Pi and on Claude Code start, reply, stop, join tasks and are deleted as before, and every text in the console is the same.
- Inside, each runtime is now one entry in a registry (its name, the model providers it runs, what it can do) plus one driver, and the console decides what to show from those capabilities instead of asking which runtime it is. This is the preparation for running Agents on other CLIs installed on your computer.
- API: new `GET /runtime-definitions` returns each runtime's name, model providers and capabilities without checking this computer (`GET /runtimes` still reports whether a runtime is installed and logged in). In run timelines, the startup phases and the process entries now always say which runtime they belong to; older records get it when read.
- For contributors: a runtime id may be compared only in the registry and the driver files (Go) and in `web/src/lib/runtime.ts` (console); CI checks both.

### Documentation

- The changelog is now written in English first, with the Chinese version in `CHANGELOG.zh-CN.md`; GitHub Release notes show the English section followed by the Chinese one. `AGENTS.md` is English with `AGENTS.zh-CN.md` beside it, and `MAIN.md` is English.

## v0.2.6

The console redesign continues: the Agent's Config, Folder and Inbox tabs, the project's overview, tasks and settings, and the five global pages are all on the sheet now; the conversation takes up more room in wide windows; tasks have a per-project number (#12); and a fix for a message sent right after a reply ends being treated as if the Agent were busy.

### Console redesign (step 2)

- The Agent's "Config", "Folder" and "Inbox" tabs are now on the sheet too, in the same style as the chat page.
- The config page is split into numbered sections (01 Basics, 02 Prompt, ...), with a contents ruler in the stub on the right where the current section is marked with yellow highlighter; the note at the top about when changes take effect is now a yellow sticky note; the thinking level is now a segmented button (in the new-Agent dialog too). Input boxes across the whole console now have a thick outline.
- Folder page: the toolbar sits at the top of the sheet, "No AGENTS.md yet" is a prominent card, the file list shows sizes and times in monospace, and the stub shows the number of folders, the number of files and the total size (with a "+" after the number while reading is not finished). The project folder page uses the same style.
- Inbox page: unread is a tilted stamp, read is a small label with the time it was read, and the stub shows the unread count and the total.
- Scrollbars inside the sheet are now thin and navy.

### Wider chat page

- The conversation takes up more room in wide windows: the maximum width of reply text grows from 760px to 1100px, and code blocks, tables and images widen with it; in an Agent conversation, the messages you send can take up to 88% of the conversation column.
- The Agent page header is shorter: the title is smaller, and the identity, runtime, status, model and address are merged into one line to the right of the title; this is the same on all four tabs.
- The turn-number column on the left is narrower, and the space above and below the input area is tighter.
- Narrow windows (when the sidebar is collapsed) are unchanged.

### Console redesign (step 3)

- The project's overview, task list and project settings are now on the sheet too; project pages and global pages share one compact header row: breadcrumb and large title, with the description and live status next to the title.
- Overview: Run status, Usage, and Issues and failures are numbered sections 01-03; status cards have a thick outline and large numbers; usage bars are filled with halftone dots (blue for input, pink for output), and the row with the highest usage gets a yellow "Most" mark; the time range and grouping are now segmented buttons. Messages under "Needs you" are still at the very top.
- Task list: the status filter (All, In progress, Blocked, Completed) becomes index tabs along the top edge of the sheet, each with a count; each task is one row showing its status, current node, responsible Agent and update time, and blocked is a tilted red stamp; the stub shows three large numbers for in progress, blocked and completed. The filter applies only to tasks that have been loaded; All and Completed show a "+" after the number when not everything is loaded.
- Project settings: the eight parts are numbered sections 01-08, with a contents ruler in the stub on the right.
- Sidebar: the selected Agent's name is no longer truncated (its badges wrap to a second line when they do not fit); in short windows the sidebar's scrollbar is now a thin white bar on blue.

### Console redesign (step 4)

- The five global pages (Global settings, External identities, Plugin market, Skills library and Stats) are now on the sheet too, with the same compact header as the project pages.
- Global settings: the seven parts are numbered sections, with a contents ruler in the stub on the right; what can be changed is written on a yellow sticky note at the top; the language is now a segmented button. Switches are yellow when on, consistently across the console.
- External identities: the list and the details are on the same sheet, and the selected identity is a yellow block; the token risk warning is now a yellow sticky note; the token table lies directly on the sheet, and "Revoke" is red.
- Plugin market: Installed, Official market and Other markets become index tabs along the top edge of the sheet; plugin cards have a halftone band at the top that rotates through blue, pink and yellow; the "Official" mark is now a small dark pill, consistently across the console.
- Skills library: template cards are no longer tilted, the empty state has been redrawn, and "Create online" is the only pink button.
- Stats: key metrics are cards with large numbers, and the shadow color tells them apart (pink for failed and interrupted, yellow for waiting for your answer, blue for total tokens); each chart is a numbered section, and no chart or number has been removed.

### Task numbers

- Every task now has a number within its project, shown as #12: the task list has a new first column, "No.", and the number also appears before the title in the task details.
- Numbers start at 1 in each project, are assigned when a task is created and never change afterwards; when a task is deleted, its number is not reused for another task. On upgrade, existing tasks are numbered in creation order.
- Task objects in the API gain a `number` field (in both the console API and the Agent API).

### Fixes

- A message sent right after an Agent's reply ended (or right after `/compact` returned) was occasionally treated as if the Agent were still busy and was queued for nothing; a session command sent at that moment failed outright with "The Agent isn't idle". The end of a reply is now announced only once the Agent is really free (PR #54).
- API: `AgentBusyDetails.currentRunId` is null when no run is in progress or queued, instead of an all-zero ID.

## v0.2.5

The console redesign begins (step 1: the shell and the Agent's chat page), and the chat page gets a new ruler with turn numbers; built-in commands in the chat box manage the session; and a fix for an Agent occasionally showing "Replying" forever.

### Console redesign (step 1)

- The console starts moving to a new design. The style is still risograph, with the same colors and fonts; this step changes the shell and the Agent's chat page.
- The main part of the page sits on a "sheet": a white surface, a navy outline and a solid blue shadow, on a background with a little paper grain. The Agent's "Chat", "Config", "Folder" and "Inbox" are now index tabs attached to the top edge of the sheet.
- The header title is larger, and the top right corner gains a decoration of overprinted blue, pink and yellow; the model and address are merged into one line, and copying the address is now an icon button.
- The chat page's ruler has been redesigned as an actual ruler, placed in the "stub" on the right of the sheet: one tick per turn, a long tick with a number every 5 turns, and the total number of turns at the bottom; the position you are viewing is marked with a stroke of yellow highlighter. It also replaces the scrollbar; scrolling with the wheel, trackpad and keyboard is unchanged. Hovering for a preview and clicking to jump work as before.
- Each turn shows its number on the left. Numbering is continuous across the whole conversation and does not restart when the session is reopened; when you scroll up and earlier messages load, the existing numbers do not change.
- The input area is merged into the bottom of the sheet, and the send button is a pink round stamp; the key hints move to a line below the input box.
- The "Session switched" divider is now a cut line with scissors; the "Run log" is a receipt in segments; bold words at the start of list items in replies are shown as small yellow labels.
- The sidebar's project cards and selected row now share the same outline and shadow, and the empty area gains a patch of halftone dots.

### Session commands in the chat

- Built-in commands in the Agent's chat box manage the session (contributed by external contributor airob0t, PR #25): `/session` shows the current session, model and process state; `/new` starts a new session and keeps the page history; `/compact [instructions]` compacts the context; `/reload` stops the current process, and the next message reloads it; `/copy` copies the latest reply that has content; `/export [html|jsonl]` exports the session; `/resume [session-id]` lists and resumes older sessions that were kept, switching only the model context without rolling back files or config. These commands are not available in task node conversations.
- Typing "/" lists the commands; those that cannot be used right now are listed separately in a "Not available now" group, each with its reason. Selecting a command only fills it into the input box and does not run it; `/new` and resuming a session ask for confirmation first.
- Agents running on Claude Code support `/session`, `/new`, `/reload` and `/copy`; `/compact`, `/export` and `/resume` need a Pi session and report right away that they are not supported.
- When there is too little content to compact or the session was just compacted, the command says that there is no need to compact, and this does not count as a failure; the compaction result is recorded on that run and is still visible after refreshing the page. Tokens used by compaction are counted under "Your messages" in Stats.
- HTML export uses Pi's own exporter and no longer starts the Agent for it; the exported page contains only the conversation, without the system prompt or tool definitions.
- When the browser does not allow writing to the clipboard (for example when the console is opened at an http LAN address), `/copy` puts the content in a read-only box, where you can click "Copy" again or select the text and copy it by hand.
- In "Issues and failures" on the overview, a failed manual compaction is labeled "Compaction failed"; clicking "View compaction" shows a note card on the chat page at the place where it happened, with the run log for it.
- After an Agent is switched from Pi to Claude Code, the command menu no longer lists the Pi commands left over from before.

### Fixes

- An Agent would occasionally keep showing "Replying" after its reply had ended: messages sent to it were rejected or stayed in the queue, and only restarting Loop Now fixed it. The cause was that background reconciliation put the busy mark back on a reply that had just ended, and nothing removed it afterwards; now it checks once more after restoring the mark, removes it immediately if the reply has already ended, and goes on to handle the queued messages.

## v0.2.4

The Agent chat page gains a message ruler: see at a glance how many turns the conversation has and where you are, and click to jump to a turn.

### Console

- A ruler now runs along the right of the message list on the Agent chat page: each tick is one turn of the conversation, and the ticks for the turns visible on screen are darker. Hovering over a tick, or moving to it with the keyboard, shows the beginning of the message you sent in that turn; clicking jumps to that turn. With many turns the ticks first get denser, and with even more the ruler scrolls on its own and follows the current position. It covers only the turns that have been loaded, and ticks are added as you scroll up and earlier messages load. It is hidden when there are fewer than two turns or the window is narrow (left sidebar collapsed).

## v0.2.3

Agents can run on the Claude Code installed on this computer; plugins can bring model providers and subscription logins, the first being the community extension pi-grok-cli; and the fixed text the platform gives to models is now in English.

### Agents can run on the local Claude Code

- Besides the bundled Pi, an Agent can now be run by the Claude Code installed on this computer. Choose "Claude Code" as the Agent's model provider; the models are fable, opus, sonnet and haiku (each follows Claude Code to the latest model of its family), and the thinking level goes from low to max. Both Agents in a project and Agents that belong to no project are supported.
- It uses your own Claude Code login on this computer: Loop Now only starts the unmodified local `claude`, does not read or store its login information, and does not offer a way to "log in to Claude inside Loop Now". Variables such as `ANTHROPIC_API_KEY` in the environment are not passed to it. You need to install Claude Code on this computer and log in from a terminal first; "Global settings > Agent runtimes" shows whether it was found, its version and its login status.
- Such an Agent runs with your real home directory: when it runs commands such as gh or git it uses your own logins, as if "Use local logins" were turned on for it alone (regardless of that global switch). Like Pi Agents, it runs commands directly without asking for confirmation. The Agent's own folder, task copies and reclaiming are unchanged.
- Your personal Claude Code settings are not loaded (CLAUDE.md, hooks, MCP, plugins, built-in skills, auto memory), and the only skills the Agent sees are those the platform provides. Only tools that act on the Agent itself are kept (running commands, reading and writing files, skills, subtasks, web search); sending messages to other Claude sessions on this computer, mobile push notifications, cloud routines, scheduled wake-ups and the like are not provided, and scheduling and notifications use Loop Now's own sleep and inbox.
- Plugins: skills and MCP tools work; Pi extensions do not apply, so there are no `/pi:` commands and no model providers from plugins, and rules injected through extensions are not available either. Plugins that contain only Pi extensions are marked in the Agent's plugin grants.
- Switching the model provider between Pi and Claude Code starts a new session: the original session is kept but not carried over, and pending sleeps are cancelled. You are asked to confirm before saving; you cannot switch while the Agent is replying.
- Claude Code writes its session records itself under `~/.claude/projects/`, separated by the Agent's working directory. When an Agent is deleted or a task is reclaimed, Loop Now first archives these records into its own data directory and then uses `claude purge` to remove just that copy, leaving the records of your everyday Claude Code use untouched.
- The run log shows the status of the subscription limit (such as the 5-hour limit and its reset time). Usage is recorded as one entry per model per turn, and a turn that was interrupted records no usage.
- Known limitations: verified only on macOS with Claude Code 2.1.322, and not verified on Windows; Claude Code Agents in tasks and plugins' MCP tools have only passed automated tests and have not been verified with a real Claude Code; if Claude Code's arguments or output change after it is upgraded, Loop Now may need an update.

### Local mode

- A fourth switch, "Use local logins", is added. It is off by default (and stays off after upgrading) and asks for confirmation once when turned on. Once on, Agents directly use your logins for the tools on this computer, which is the same as acting as you, and all Agents share them (Agents in tasks too); CLIs that already have an account configured for the Agent in Loop Now still use the configured account. It is independent of "Use local CLIs": with only this one on, only commands Agents could already run can use these logins.

### Model providers from plugins

- Pi extensions in the plugin market can bring model providers and subscription logins. After you confirm an install (or update), Loop Now reads the providers and models the extension registers and lists them under "Model providers" in the plugin details; providers that come with a login enter the model catalog.
- Subscription accounts are managed per project: under "Model accounts" in project settings, choose the plugin's provider and log in (browser login or device code; the extension decides which methods exist), then select the account in the Agent config. The plugin must first be granted to the Agent before its provider can be chosen in the model config; it cannot be chosen directly when creating an Agent, so create the Agent and change it afterwards.
- The login state is saved and refreshed by the extension itself and kept in Loop Now's data directory (readable only by the current user); it is not stored in the database or in backups; Agents in the same project that use the same account share one copy. A task keeps using the login from when it started, and logging in again does not affect tasks in progress.
- Uninstalling a plugin keeps each project's subscription accounts by default, and they are restored after reinstalling; they are deleted as well when "Also delete the data folder" is checked.
- With "Use local logins" on, such providers use the extension's own login on this computer rather than the project account; when this computer has no such login, starting the Agent fails right away with an error that explains why.
- The first one available is the community extension pi-grok-cli (a SuperGrok or X Premium subscription; not official from xAI), installed from the official plugin market.
- Agents that do not belong to any project do not support such providers yet.

### Fixed text given to models is now in English

- All fixed text the platform gives to models is now in English: the paragraphs the platform appends to the system prompt (folders and context, local directories, local logins and so on), the run ID line at the start of each message, the notices and reminders the platform sends to Agents (sleep due, task node started, Inbox reminders, question status, task notices to the Manager and so on), loopctl's skill instructions and command descriptions, and the prompts official plugins give to models. These used to be in Chinese, so an Agent you spoke to in English often replied in Chinese; newly started sessions now follow the language you use. In existing conversations the history is still in Chinese, and the model may keep replying in Chinese.
- Error messages, failure reasons and UI copy are unchanged.
- Side effect: a few pieces of platform text that are both read by the model and shown in the console (the body of platform reminders in a conversation, task notices, the "question has expired" notice, the original-recipient note on forwarded messages) appear in English in the Chinese UI as well.

### Console

- Typing "/" in the chat input box brings up Pi's command list (extension commands, skills, prompt templates), shown as `/pi:<name>`; choose with ↑↓ and fill in with Enter or Tab. Sending `/pi:<name> arguments` makes Pi run that command; when the command handles everything itself and there is no model reply, the turn shows "Command ran; no reply". The list is available only after the Agent has run once. A `/xxx` without `pi:` works as before: it is first offered to plugins to claim, and if none claims it, it is sent to the model as ordinary text.
- In a project, the number of running tasks is shown next to "Tasks" in the left sidebar (preparing, waiting and blocked tasks are not counted); nothing is shown when it is 0. It updates automatically within a few seconds after a task starts or ends, and immediately when you open the tasks page, with no need to refresh the page.
- The "Pi process" column on the overview is renamed "Process", and "Pi cold starts / Pi crashes" on the Stats page become "Cold starts / Process crashes": these numbers cover the processes of both runtimes.

### Fixes

- Windows: an Agent process that had just exited was occasionally misjudged as still running, which made a stop or handover report `runtime_stop_unconfirmed`.
- Run timeline: when the last tool call and the end fall at the same instant, the "end" entry is no longer placed before the tool call.

### Server

- New `GET /agents/{agentId}/commands` (the command list from Pi's last start) and `POST /projects/{projectId}/model-auth/operations/{operationId}/input` (answers a question asked in the middle of a login); `GET /model-providers` can take `agentId` and returns whether each plugin provider is available to that Agent and why.
- Plugin manifests and market entries gain an optional `providerState`: it declares which paths under HOME the provider has saved along with the project account.
- New `GET /projects/{projectId}/tasks/counts` (console only): returns the number of unfinished tasks in the project by status.
- New `GET /runtimes` (console only): whether each runtime is available and its version, plus the path and login status of the local Claude Code (without email or organization). Agents gain a read-only `runtime` (`pi` / `claude-code`, determined by the model provider); model providers gain `runtime`, and `source` gains `local_cli`.
- Failure reasons gain `runtime_unavailable`, `runtime_login_required`, `runtime_start_failed`, `runtime_exited` and `runtime_error` (used by the Claude Code runtime; Pi's `pi_*` are unchanged); process entries in the timeline gain an optional `runtime`, and there is a new `model_rate_limit` entry.

## v0.2.2

New "Local mode": lets Agents directly use the directories, git credentials and commands already on this computer. Each of the three has its own switch under "Global settings > Local mode"; all are on by default, and after upgrading too.

### Local mode

- Bind local directories: register local directories (an absolute path plus a note) in project settings and in the Agent config; those registered on a project apply to all Agents in the project. The Agent's own folder is unchanged; these directories are additional places it is told it can work in. The Agent's changes there land directly on the real files, in tasks too, without going through a task copy; the list only informs the Agent and is not access isolation. A directory that does not exist is marked, and the Agent is not told about it for the time being.
- Use local git identity: for "Pull from Git", HTTPS addresses can use the git credentials saved on this computer (such as in the keychain) and SSH addresses can use the keys in ssh-agent, so private repositories outside GitHub can be pulled. SSH addresses using keys under `~/.ssh` have always worked and are not affected by this switch. When the Agent has a GitHub account configured, github.com still uses only that account.
- Use local CLIs: Agents can run the commands installed on this computer (your PATH is appended after the Agent's PATH); for their logins, these commands still use the accounts configured in Loop Now. PATH is read once when the server starts, so newly installed tools are seen only after restarting Loop Now.
- When a pull fails, "Authentication failed" and "SSH host key not verified" are told apart, each with what to do next.

### Server

- `loop-server import-postgres` now first builds the baseline schema, imports and verifies the data, and only then applies the later migrations.

## v0.2.1

Fixes external identities and `loopctl` not working in the desktop app.

### Desktop app

- External identities and `loopctl` now work: the desktop app automatically opens the external TLS port on this machine (127.0.0.1); the port is chosen by the system the first time and reused afterwards, and the certificate is a self-signed one maintained by the server itself (`~/Library/Application Support/Loop Now/<profile>/shell/tls/cert.pem`). The menu bar icon gains "External Identity Connection...", which shows the address, the certificate location and the command that sets the token up in `loopctl`, each copyable with one click. `lne_` tokens created earlier do not need to be reissued. Only connections from this machine are accepted for now.

### Server

- New `LOOP_NOW_EXTERNAL_TLS_SELF_SIGNED=<dir>`: the external port uses a self-signed certificate that the server maintains in that directory (`cert.pem` and `key.pem`, generated when missing or close to expiry, covering the loopback addresses and the host the external listener is bound to); mutually exclusive with `LOOP_NOW_EXTERNAL_TLS_CERT` / `LOOP_NOW_EXTERNAL_TLS_KEY`. When the external port is open, the ready file gains an `externalUrl` entry.

## v0.2.0

This version switches storage to SQLite, no longer needs PostgreSQL or Redis, and introduces the macOS desktop app. Upgrading from v0.1.0 requires a one-time data import; see "Upgrading from v0.1.0" below.

### Desktop app (macOS Apple Silicon)

- One DMG carries the server, the console, Node 24.21 and Pi 1.4.0; nothing else needs to be installed. Download `loop-now-desktop-<version>-mac-arm64.dmg` and drag the app into "Applications".
- The menu bar icon shows the running status and lets you open the window, open in the browser, set launch at login, view logs, restart and quit. When you close the window, the server and the Agents keep running in the background.
- Alerts from the page appear as system notifications and as an unread count on the Dock; external links open in the system browser.
- Data is kept separate from a source installation: the database and daily backups are in `~/Library/Application Support/Loop Now/default/data`, Agent workspaces in `/Users/Shared/loop-now-desktop/default`, and logs in `~/Library/Logs/Loop Now/default`. `open -a "Loop Now" --args --profile=<name>` runs it with a separate, independent set of data.
- The package is not yet notarized by Apple: the first time you open it, go to "System Settings > Privacy & Security" and click "Open Anyway". "Check for updates" in the menu bar only announces a new version and opens the download page; it does not install automatically.
- Only an Apple Silicon build exists for now. The external TLS port that external identities and `loopctl` need cannot be opened in the desktop app yet.

### Storage uses SQLite only

- Running needs only one `loop-server` and one data directory (`LOOP_NOW_DATA_DIR`, default `~/.loop_now`). The database is under `db/` inside it and is created automatically on first start.
- A backup is made automatically every day to `<data dir>/backups`, keeping the latest 7 (changeable with `LOOP_NOW_BACKUP_DIR` and `LOOP_NOW_BACKUP_KEEP`); `loop-server backup <dir>` makes a manual backup at any time, even while the server is running.
- Credentials are still stored in plain text in the database file in the data directory, and other processes of the same user can read them directly. Protect the data directory and the backups.

### Release bundles

- Each version provides server release bundles on GitHub Releases, for macOS (Apple Silicon, Intel) and Linux (x86_64, ARM64). A bundle contains `loop-server` with the console built in, `loopctl`, the plugin host, the official plugins, plus Node 24.21.0 and Pi 1.4.0, so after unpacking there is no need to install Node or Pi separately. The console is served at the root path of the server port (default `http://127.0.0.1:8780/`).
- Checksums for all files are in `SHA256SUMS`.

### Console

- Project settings > Model accounts: the provider is now chosen from a searchable panel. Providers are grouped by purpose and show their name, aliases and regional variants; providers that already have an account come first. Providers that need only an API key open the form directly, and Codex goes straight to login. Account rows show the provider name, id and authentication method, and the update time is formatted for the console's language.
- The plugin details page and the preview before installing or updating show the plugin's README (chosen by the console language); plugins that come from git show a repository link. All official plugins have READMEs in Chinese and English.
- The console supports English: it follows the browser language by default, can be switched in "Global settings", and can also be set with `?lang=`.
- Global settings > Model settings can edit the native `models.json` of the pinned Pi version, to add custom providers or override built-in ones. `apiKey` and `headers` accept only environment variable references of the form `$NAME`; real credentials are still configured in the project's model accounts.

### Plugins and tools

- `mcpServers` in the plugin manifest supports streamable HTTP entries (`http://` or `https://` addresses).
- A plugin's `displayName` and `description` can be written per language, and the console shows them in the current language.
- When replying in a plugin's thread, an Agent can ask a question with options, and the plugin answers with the SDK's `api.channels.answer`; the `config.projects` a plugin receives contains only the projects that have it enabled.
- The mysql and gcloud CLI tools support Linux/arm64.
- New all-in-one Docker Compose setup, with ports bound only to the local loopback address.

### Upgrading from v0.1.0

v0.1.0 keeps its data in PostgreSQL, and v0.2.0 needs it imported into SQLite once. The import only reads the old database and does not modify it, so if you need to roll back, the old version can still run on the old database (data created after the switch does not go back into the old database).

1. The import requires the old database to be migrated to the last migration of the PostgreSQL version, and v0.1.0 is two migrations short of that. First check out the tag `pg-final` (the last commit of the PostgreSQL version), build from source, start it once with your original configuration, and stop the server once the migrations have finished.
2. With v0.2.0's `loop-server`: point `LOOP_NOW_DATA_DIR` at a data directory that has no database yet (the default `~/.loop_now` is fine as long as it has no `db/` yet), set `LOOP_NOW_DATABASE_URL` to the old database, and run `loop-server import-postgres` once. It copies all data within one consistent snapshot, verifies row counts, foreign keys and integrity, and prints the row count of every table; if any step fails, no new database is left behind.
3. Keep your original `LOOP_NOW_WORK_ROOT` and start v0.2.0 on this data directory. Redis is no longer needed after that.

### Breaking changes

- PostgreSQL and Redis are no longer used. `LOOP_NOW_REDIS_URL`, `LOOP_NOW_REDIS_KEY_PREFIX`, `LOOP_NOW_LOCK_TTL`, `LOOP_NOW_LOCK_RENEW_INTERVAL` and `LOOP_NOW_RUN_TEXT_TTL` have been removed and are no longer read even if set; `LOOP_NOW_DATABASE_URL` is read only by `loop-server import-postgres` during the import.
- The health endpoint `GET /api/v1/health` now returns `{ok, database}`: the `postgres` and `redis` fields are gone, and `database` carries the database status, WAL size and warnings. `loopctl health` now shows the database status as well.
- A data directory and a work root can each be used by only one server at a time. Exit codes for startup failures are fixed: 2 for a configuration error, 3 when the data directory or work root is in use by another server, 4 when the listen address is in use, and 1 for other errors.
- A reply in progress is kept only in memory: when the server is stopped normally, the reply ends with the content generated so far and is marked as interrupted; when the server crashes, that content is lost (it used to be stored in Redis).
- Lifecycle events, tool call events and audit logs are kept only for the latest 90 days and are cleaned up once a day.
- The default organization and default project created by a new installation are both named `default` (previously “默认组织” and “默认项目”, Chinese for "Default organization" and "Default project"); existing data is not affected.
