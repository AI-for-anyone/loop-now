#!/usr/bin/env bash
# Tests for the change notes scripts (collect-changes.sh, notes.sh on "Unreleased", check-change-notes.sh). Each case
# works in a scratch git repository with small CHANGELOG files and MAIN.md and a copy of the scripts, never in this
# checkout. Bash 3.2 (macOS) and Linux.
#
#   scripts/release/change-notes-test.sh
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
failures=0

fail() {
	echo "FAIL: $*" >&2
	failures=$((failures + 1))
}

g() {
	git -c user.name=test -c user.email=test@example.com -c commit.gpgsign=false "$@"
}

# commit_at SECONDS MESSAGE: commits everything with author and committer time SECONDS (since the epoch).
commit_at() {
	GIT_AUTHOR_DATE="@$1 +0000" GIT_COMMITTER_DATE="@$1 +0000" g add -A
	GIT_AUTHOR_DATE="@$1 +0000" GIT_COMMITTER_DATE="@$1 +0000" g commit -q -m "$2"
}

# new_repo NAME: a scratch repository with the scripts and the three files, committed on main; prints its path.
new_repo() {
	local dir="$scratch/$1"
	mkdir -p "$dir/scripts/release" "$dir/changes"
	cp "$here"/*.sh "$dir/scripts/release/"
	cat >"$dir/CHANGELOG.md" <<'EOF'
# Changelog

Preamble.

## Unreleased

### Old entry

- Old bullet.

## v0.1.0

### First release

- Released.
EOF
	cat >"$dir/CHANGELOG.zh-CN.md" <<'EOF'
# 更新日志

前言。

## 未发布

### 旧条目

- 旧的一条。

## v0.1.0

### 第一版

- 发布了。
EOF
	cat >"$dir/MAIN.md" <<'EOF'
# Project

## Current design direction

- An old decision.
  - Its detail.
<!-- New change entries are inserted below this line by scripts/release/collect-changes.sh, newest first. -->
- The newest entry written by hand.
  - Its detail.

## Delivered and unfinished

Text.
EOF
	printf 'Change notes.\n' >"$dir/changes/README.md"
	(cd "$dir" && g init -q -b main && commit_at 1000000000 base)
	echo "$dir"
}

# note FILE TITLE: writes a complete change note.
note() {
	cat >"$1" <<EOF
## English

### $2

- What changed in $2.

## 中文

### $2（中文）

- $2 的变化。

## MAIN.md

- $2 (2026-11-26; the user: “做吧” ("Do it")).
  - How $2 was verified.
EOF
}

# ---- collect-changes.sh ----

repo="$(new_repo collect)"
note "$repo/changes/zeta-older.md" "Older"
(cd "$repo" && commit_at 1000000100 older)
note "$repo/changes/alpha-newer.md" "Newer"
(cd "$repo" && commit_at 1000000200 newer)

if ! out="$("$repo/scripts/release/collect-changes.sh" v0.2.0 2>&1)"; then
	fail "collect with two notes failed: $out"
fi
expected_en='## Unreleased

### Newer

- What changed in Newer.

### Older

- What changed in Older.

### Old entry

- Old bullet.

## v0.1.0'
if [ "$(awk '/^## Unreleased/,/^## v0.1.0/' "$repo/CHANGELOG.md")" != "$expected_en" ]; then
	fail "CHANGELOG.md after collecting:
$(cat "$repo/CHANGELOG.md")"
fi
expected_zh='## 未发布

### Newer（中文）

- Newer 的变化。

### Older（中文）

- Older 的变化。

### 旧条目

- 旧的一条。

## v0.1.0'
if [ "$(awk '/^## 未发布/,/^## v0.1.0/' "$repo/CHANGELOG.zh-CN.md")" != "$expected_zh" ]; then
	fail "CHANGELOG.zh-CN.md after collecting:
$(cat "$repo/CHANGELOG.zh-CN.md")"
fi
expected_main='- An old decision.
  - Its detail.
<!-- New change entries are inserted below this line by scripts/release/collect-changes.sh, newest first. -->
- Newer (2026-11-26; the user: “做吧” ("Do it")).
  - How Newer was verified.
- Older (2026-11-26; the user: “做吧” ("Do it")).
  - How Older was verified.
- The newest entry written by hand.
  - Its detail.

## Delivered and unfinished'
if [ "$(awk '/^- An old decision/,/^## Delivered/' "$repo/MAIN.md")" != "$expected_main" ]; then
	fail "MAIN.md after collecting:
$(cat "$repo/MAIN.md")"
fi
if [ -e "$repo/changes/zeta-older.md" ] || [ -e "$repo/changes/alpha-newer.md" ]; then
	fail "collected notes were not deleted"
fi
if [ ! -e "$repo/changes/README.md" ]; then
	fail "changes/README.md was deleted"
fi

# A second run changes nothing.
sums() { cat "$repo/CHANGELOG.md" "$repo/CHANGELOG.zh-CN.md" "$repo/MAIN.md" | cksum; }
before="$(sums)"
if ! out="$("$repo/scripts/release/collect-changes.sh" v0.2.0 2>&1)"; then
	fail "second collect failed: $out"
fi
[ "$(sums)" = "$before" ] || fail "second collect changed the files"

# A note that is not committed yet counts as the newest.
note "$repo/changes/uncommitted.md" "Uncommitted"
"$repo/scripts/release/collect-changes.sh" v0.2.0 >/dev/null 2>&1 || fail "collect with an uncommitted note failed"
first_title="$(awk '/^## Unreleased/{f=1;next} f && /^### /{print;exit}' "$repo/CHANGELOG.md")"
[ "$first_title" = "### Uncommitted" ] || fail "the uncommitted note is not first: $first_title"

# A note missing a section is refused by name, and nothing is written.
repo="$(new_repo refuse)"
note "$repo/changes/good.md" "Good"
cat >"$repo/changes/no-chinese.md" <<'EOF'
## English

### No Chinese

- A bullet.

## MAIN.md

- An entry.
EOF
before="$(cat "$repo/CHANGELOG.md" "$repo/CHANGELOG.zh-CN.md" "$repo/MAIN.md" | cksum)"
if out="$("$repo/scripts/release/collect-changes.sh" v0.2.0 2>&1)"; then
	fail "a note without 中文 was accepted"
fi
case "$out" in *changes/no-chinese.md*中文*) ;; *) fail "the refusal does not name the file and the section: $out" ;; esac
[ "$(cat "$repo/CHANGELOG.md" "$repo/CHANGELOG.zh-CN.md" "$repo/MAIN.md" | cksum)" = "$before" ] || fail "a refused collect wrote files"
[ -e "$repo/changes/good.md" ] || fail "a refused collect deleted a note"

# Sections out of order, an unknown section, or an empty one are refused too.
printf '## 中文\n\n### A\n\n- a\n\n## English\n\n### A\n\n- a\n\n## MAIN.md\n\n- a\n' >"$repo/changes/no-chinese.md"
out="$("$repo/scripts/release/collect-changes.sh" v0.2.0 2>&1)" && fail "sections out of order were accepted"
printf '## English\n\n### A\n\n- a\n\n## 中文\n\n## MAIN.md\n\n- a\n' >"$repo/changes/no-chinese.md"
out="$("$repo/scripts/release/collect-changes.sh" v0.2.0 2>&1)" && fail "an empty section was accepted"
case "$out" in *changes/no-chinese.md*中文*) ;; *) fail "the empty section is not named: $out" ;; esac
rm "$repo/changes/no-chinese.md"

# A version that is not one is refused.
out="$("$repo/scripts/release/collect-changes.sh" next 2>&1)" && fail "the version \"next\" was accepted"

# Without an "Unreleased" section, collecting creates one above the newest version.
repo="$(new_repo create-section)"
awk '/^## Unreleased/{skip=1;next} /^## /{skip=0} !skip' "$repo/CHANGELOG.md" >"$repo/x" && mv "$repo/x" "$repo/CHANGELOG.md"
awk '/^## 未发布/{skip=1;next} /^## /{skip=0} !skip' "$repo/CHANGELOG.zh-CN.md" >"$repo/x" && mv "$repo/x" "$repo/CHANGELOG.zh-CN.md"
note "$repo/changes/one.md" "One"
"$repo/scripts/release/collect-changes.sh" v0.2.0 >/dev/null 2>&1 || fail "collect without an Unreleased section failed"
expected='Preamble.

## Unreleased

### One

- What changed in One.

## v0.1.0'
[ "$(awk '/^Preamble/,/^## v0.1.0/' "$repo/CHANGELOG.md")" = "$expected" ] || fail "no Unreleased section was created:
$(cat "$repo/CHANGELOG.md")"
grep -q '^## 未发布$' "$repo/CHANGELOG.zh-CN.md" || fail "no 未发布 section was created"

# ---- notes.sh Unreleased: the collected section plus the notes not collected yet ----

repo="$(new_repo notes)"
note "$repo/changes/pending.md" "Pending"
if ! out="$("$repo/scripts/release/notes.sh" Unreleased 2>&1)"; then
	fail "notes.sh Unreleased failed: $out"
fi
expected='### Pending

- What changed in Pending.

### Old entry

- Old bullet.

---

## 中文

### Pending（中文）

- Pending 的变化。

### 旧条目

- 旧的一条。'
[ "$out" = "$expected" ] || fail "notes.sh Unreleased printed:
$out"
# A released version is printed as before, without the pending notes.
out="$("$repo/scripts/release/notes.sh" v0.1.0 2>&1)" || fail "notes.sh v0.1.0 failed: $out"
case "$out" in *Pending*) fail "notes.sh v0.1.0 shows a pending note: $out" ;; esac
case "$out" in *"### First release"*"## 中文"*"### 第一版"*) ;; *) fail "notes.sh v0.1.0 printed: $out" ;; esac
# A bad note makes the preview fail instead of leaving it out.
printf '## English\n\n### Bad\n\n- b\n' >"$repo/changes/bad.md"
out="$("$repo/scripts/release/notes.sh" Unreleased 2>&1)" && fail "notes.sh Unreleased passed with a bad note: $out"
case "$out" in *changes/bad.md*) ;; *) fail "notes.sh Unreleased does not name the bad note: $out" ;; esac

if [ "$failures" -gt 0 ]; then
	echo "$failures change notes test(s) failed" >&2
	exit 1
fi
echo "change notes tests: ok"
