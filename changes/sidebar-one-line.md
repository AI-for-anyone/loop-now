## English

### Sidebar Agent rows stay on one line

- An Agent row in the sidebar is always one line: a long name ends with an ellipsis (the full name is in the tooltip) instead of pushing the runtime mark and the status onto a second line.
- The status is a small symbol instead of a word: a pulsing pink dot while replying, a yellow pill with a shell glyph and the count while background work runs, a moon while sleeping, a flag for the project's Manager. The words are in the tooltip and for screen readers; without motion the dot does not pulse.
- The other sidebar rows (projects, groups, the project switcher, the connection line) stay on one line too, with tooltips for cut text.

## 中文

### 左边栏的 Agent 每个只占一行

- 左边栏里每个 Agent 永远只占一行：名字太长就显示省略号（鼠标停上去看全名），不再把运行时图标和状态挤到第二行。
- 状态从文字改成小图标：回复中是一个会轻轻闪的粉色圆点，后台有活是带 shell 图标和数量的黄色小块，休眠是月亮，项目的 Manager 是小旗子。文字放在鼠标提示和读屏文字里；系统关掉动画时圆点不闪。
- 左边栏的其它行（项目、分组标题、项目切换、连接状态）也都只占一行，被截断的文字有鼠标提示。

## MAIN.md

- Sidebar Agent rows stay on one line (2026-11-26; looking at the sidebar where a replying Agent took two lines, name above and the runtime mark with “回复中” below, the user said “这种挤成两行也太丑了” ("Squeezed into two lines like this is too ugly")). Task #14, branch `task/sidebar-one-line` (loop-now-web implemented, loop-now-main verified). Every sidebar row has a fixed one-line height (`lib/nav-row.ts`); the Agent name truncates; `StatusTag` in `features/agents/AgentList.tsx` shows one symbol by the old priority (replying pink dot, background pill with count, sleeping moon, Manager flag shared with `ManagerBadge`), with the words in the tooltip and screen-reader text; the sidebar runtime mark is 16 px. Verified with the web checks (1839 tests) and on a temporary instance with real Claude Code Agents in each state (replying, two background commands, sleeping, Manager), desktop and phone drawer, Chinese and English.
