## English

### The page body uses the whole window

- The body of every console page (an Agent's four tabs, a task, the task list, overview, settings, stats, plugins, Skills, identities, import) no longer sits on an inset sheet with a thick border, rounded corners and a drop shadow: it meets the header, the sidebar and the window edges, with a thin ink line under the header and between the sidebar and the page.
- A conversation uses the width up to the turn ruler (24 px gutters, up to 1600 px); tables and code blocks use all of it, and the message box lines up with the messages. On a 1920 px window the space left of the ruler went from about 360 px to about 37 px. The turn ruler is 40 px wide, and there is less space above the first message and above the message box.
- Task node conversations get the same width; phones keep their layout with 14 px gutters.

## 中文

### 页面内容用满整个窗口

- 控制台各页面的内容区（Agent 的四个标签页、任务页、任务列表、总览、设置、统计、插件、Skills、外部身份、导入）不再放在一张带粗边框、圆角和阴影、四周留空的"纸"上，而是直接贴着顶栏、左边栏和窗口边缘，顶栏下面和左边栏旁边各留一条细线。
- 对话的消息铺满到右边的轮次标尺（两边各 24 像素，最宽 1600 像素），表格和代码块用满整个宽度，输入框和消息对齐。1920 像素宽的窗口里，消息右边空出的地方从约 360 像素变成约 37 像素。轮次标尺变窄到 40 像素，第一条消息上方和输入框上方的留白也收紧了。
- 任务节点的对话同样加宽；手机上布局不变，两边留 14 像素。

## MAIN.md

- The page body uses the whole window (2026-11-26; with a screenshot of a chat after the thin header bar, the user said “为什么一定要浪费这么多空间，留这么大的边框呢” ("Why must so much space be wasted, with such a big border?")). Task #18, branch `task/chat-full-width`: `Sheet.tsx` lost its thick border, radius, offset shadow and outer margins (a 1.5 px top line remains), `AppLayout` draws a 1.5 px line between sidebar and page, gutters moved into `PageHeader` and the pages; chat and task node conversations go up to 1600 px with 24 px gutters (14 px on phones), the turn ruler (`MessageRuler`) is 40 px. Verified by loop-now-main on a temporary instance with a real Claude reply holding a 9-column table, a long paragraph and a long code line: at 1920 px the table and code reach 1845 px and the ruler starts at 1882 px (about 37 px unused, about 360 px before); screenshots at 1440, 1920 and 390, zh and en, of chat, a task node, tasks, settings and config.
