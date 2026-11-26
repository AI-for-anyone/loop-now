# Change notes

中文：[README.zh-CN.md](README.zh-CN.md)

Every change that users or maintainers should hear about adds exactly one new file here, `changes/<slug>.md`, instead of
editing `CHANGELOG.md`, `CHANGELOG.zh-CN.md` or the change list in `MAIN.md`. The slug is the branch name without
`task/`: lower-case ASCII letters, digits and hyphens. Pull requests waiting in the merge queue then never touch the same
lines. When a release is cut, `scripts/release/collect-changes.sh <version>` on the release branch moves every note into
those three files and deletes the note files; `scripts/release/notes.sh Unreleased` previews the next release notes
before that. CI fails a pull request that adds lines under "## Unreleased" / "## 未发布" or a new entry to MAIN.md's
change list (release branches `release-*` excepted).

A note has exactly these three level-2 sections, in this order:

```markdown
## English

### The sidebar's width can be dragged

- What a user sees now, in one or two bullets (this is the CHANGELOG.md entry).

## 中文

### 左边栏可以拖动改宽度

- 同一条，中文（这是 CHANGELOG.zh-CN.md 里的条目）。

## MAIN.md

- The sidebar's width can be dragged (2026-11-26; the user: “然后这里要能左右拖动” ("And this here must be draggable left and right")).
  - What was built and how it was verified; what is known and not done.
```

To change a note before it is released, edit its file. Entries already in "## Unreleased" stay where they are.
