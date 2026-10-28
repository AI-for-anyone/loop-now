# Publishing to the market

The official plugin market is a Git repository: [`AI-for-anyone/loop-now-market`](https://github.com/AI-for-anyone/loop-now-market). The Loop Now server reads its index file — `loop-now-market.json` on the `main` branch — every time the market page opens or an install begins, so a merged entry is immediately installable for every user.

## How listing works

1. **Publish your plugin as a Git repository** with proper release tags. Entries in the index point at a tag, never a moving branch.
2. **Open a PR against the market repository** adding your entry to the index. Every entry is reviewed before merge: the plugin's two halves, declared events, permissions, and install source are checked.
3. **The market's CI validates each entry** with this repository's plugin host, so a broken manifest fails before it ever reaches users.
4. **Once merged, installation needs no confirmation** — that is exactly why the review step exists.

Because listing implies implicit trust, keep the plugin's manifest honest and minimal: declare only the events and permissions you actually use.

## Worked example: the Slack plugin

The first market plugin is [`AI-for-anyone/loop-now-plugin-slack`](https://github.com/AI-for-anyone/loop-now-plugin-slack):

- published as its own open repository with release tags,
- listed in the market index pinned to tag `v0.1.0`,
- connects Slack workspaces through Socket Mode (no public address needed),
- manages an external identity so agents can reach people back,
- answers the plugin-channel questions the platform delivers to it.

Use it as the reference for packaging, tagging, and preparing a market PR.

## After listing

- Versions stay pinned: users update explicitly, and running tasks keep the files they started with.
- You can also ship plugins **outside the market** — Git URL, npm package, or local directory — where install always asks for confirmation. The market is the trust shortcut, not the only distribution channel.
- To run a private market, point the server at your own index with the `LOOP_NOW_PLUGIN_MARKET_URL` environment variable.
