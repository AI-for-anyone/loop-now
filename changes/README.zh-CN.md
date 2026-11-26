# 改动说明

English: [README.md](README.md)（以英文版为准）

每个需要让使用者或维护者知道的改动，在这里新加且只加一个文件 `changes/<slug>.md`，不再直接改 `CHANGELOG.md`、
`CHANGELOG.zh-CN.md` 或 `MAIN.md` 的改动列表。slug 是分支名去掉 `task/`：小写英文字母、数字和连字符。这样在合并队列里
排队的几个 PR 不会改到同一处。发布时在发布分支上运行 `scripts/release/collect-changes.sh <版本>`，把每个说明放进这三个
文件，并删掉这些说明文件；收集之前可以用 `scripts/release/notes.sh Unreleased` 预览下一版的发布说明。PR 如果在
“## Unreleased”／“## 未发布”下加了行，或在 MAIN.md 的改动列表里加了新条目，CI 会失败（`release-*` 发布分支除外）。

一个说明文件只有下面三个二级标题，顺序固定：`## English`（CHANGELOG.md 的条目：一个 `### 标题` 加几条要点）、
`## 中文`（CHANGELOG.zh-CN.md 的条目）、`## MAIN.md`（MAIN.md 的条目：一个顶层要点和它的子要点，英文，引用用户原话）。
格式示例见英文版。

发布前要改说明，直接改它的文件。已经写在“## Unreleased”里的条目留在原处。
