#!/usr/bin/env bash
# Tests for the pull request notes scripts (collect-pr-notes.sh, notes.sh on "Unreleased", check-pr-notes.sh). Each
# case works in a scratch git repository with small CHANGELOG files and MAIN.md and a copy of the scripts, never in this
# checkout. A fake gh on PATH answers "gh pr view <n>" from fixed descriptions; nothing goes to the network. Bash 3.2
# (macOS) and Linux.
#
#   scripts/release/pr-notes-test.sh
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

# The fake gh: "gh pr view <n> ..." prints the file $FAKE_GH_PRS/<n> (the title on the first line, then the
# description), as the real gh prints for --jq '.title + "\n" + (.body // "")'. Every call is logged in $FAKE_GH_LOG.
mkdir -p "$scratch/bin"
cat >"$scratch/bin/gh" <<'EOF'
#!/usr/bin/env bash
echo "$*" >>"$FAKE_GH_LOG"
if [ "${1:-} ${2:-}" != "pr view" ]; then
	echo "fake gh: unexpected call: $*" >&2
	exit 2
fi
if [ ! -f "$FAKE_GH_PRS/$3" ]; then
	echo "no pull requests found for #$3" >&2
	exit 1
fi
cat "$FAKE_GH_PRS/$3"
EOF
chmod +x "$scratch/bin/gh"
export PATH="$scratch/bin:$PATH"
export FAKE_GH_PRS="$scratch/prs"
export FAKE_GH_LOG="$scratch/gh.log"
mkdir -p "$FAKE_GH_PRS"
: >"$FAKE_GH_LOG"

# describe N TITLE BODY: the description of pull request N.
describe() {
	printf '%s\n%s\n' "$2" "$3" >"$FAKE_GH_PRS/$1"
}

# notes_body TITLE: a description with both notes sections and the free-form ones.
notes_body() {
	cat <<EOF
## English

<!-- A "### Title" and bullets, as they will appear in CHANGELOG.md. -->

### $1

- What changed in $1.

## 中文

### $1（中文）

- $1 的变化。

## Why

Because.

## What changed

- Code.

## How it was verified

- Tests.
EOF
}

# A clock for commits, so the order is fixed.
now=1000000000
commit() { # commit MESSAGE: commits everything.
	now=$((now + 100))
	GIT_AUTHOR_DATE="@$now +0000" GIT_COMMITTER_DATE="@$now +0000" g add -A
	GIT_AUTHOR_DATE="@$now +0000" GIT_COMMITTER_DATE="@$now +0000" g commit -q --allow-empty -m "$1"
}

# merge_pr N BRANCH [WITH_SCRIPTS]: merges a one-commit branch into main as GitHub's merge queue does ("Merge pull
# request #N from owner/BRANCH"). WITH_SCRIPTS=scripts also adds the release scripts in that pull request.
merge_pr() {
	g checkout -q -b "$2"
	echo "$2" >>work.txt
	if [ "${3:-}" = scripts ]; then
		mkdir -p scripts/release
		cp "$here"/*.sh scripts/release/
	fi
	commit "work on $2"
	g checkout -q main
	merge_branch "$1" "$2"
}

# merge_branch N BRANCH: merges the existing BRANCH into main as pull request #N.
merge_branch() {
	g checkout -q main
	now=$((now + 100))
	GIT_AUTHOR_DATE="@$now +0000" GIT_COMMITTER_DATE="@$now +0000" g merge -q --no-ff -m "Merge pull request #$1 from owner/$2

$2" "$2"
}

# titles FILE HEADING: the "### " titles of the section HEADING of FILE, one per line.
titles() {
	section "$1" "$2" | grep '^### ' || true
}

# new_repo NAME: a scratch repository on main with the CHANGELOG files and MAIN.md, tagged v0.1.0, without the release
# scripts; prints its path.
new_repo() {
	local dir="$scratch/$1"
	mkdir -p "$dir"
	cat >"$dir/CHANGELOG.md" <<'EOF'
# Changelog

Preamble.

## Unreleased

### Old entry

- Old bullet.

## v0.1.0

First release summary.

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

第一版总结。

### 第一版

- 发布了。
EOF
	cat >"$dir/MAIN.md" <<'EOF'
# Project

## Current design direction

- An old decision.
  - Its detail.
<!-- Change history lives in the pull requests and CHANGELOG.md, not here: do not add entries for changes below this line. -->
- The newest entry written by hand.
  - Its detail.

## Delivered and unfinished

Text.
EOF
	(cd "$dir" && g init -q -b main && commit base && g tag v0.1.0)
	echo "$dir"
}

# section FILE HEADING: the lines of section HEADING of FILE, heading included, up to the next level-2 heading.
section() {
	awk -v h="$2" '$0 == h { on = 1; print; next } on && /^## / { exit } on' "$1"
}

sums() { cat "$1/CHANGELOG.md" "$1/CHANGELOG.zh-CN.md" "$1/MAIN.md" | cksum; }

# ---- collect-pr-notes.sh ----

# History since v0.1.0, oldest first: #1 merged before the scripts existed (its description has notes anyway: it is
# listed, never copied, because its notes reached the CHANGELOG another way), #2 adds the scripts and has notes, #3 has
# notes with Windows line ends, #4 says None / 无, #5 has no notes sections, and a release branch merge.
repo="$(new_repo collect)"
(
	cd "$repo"
	merge_pr 1 task/before
	merge_pr 2 task/the-scripts scripts
	merge_pr 3 task/crlf
	merge_pr 4 task/invisible
	merge_pr 5 task/old-style
	merge_pr 9 release-v0.0.9
)
describe 1 "Before the scripts" "$(notes_body Before)"
describe 2 "The scripts" "$(notes_body Scripts)"
describe 3 "Windows line ends" "$(notes_body Crlf | sed 's/$/\r/')"
describe 4 "Nobody sees this" "$(printf '## English\n\nNone\n\n## 中文\n\n无\n\n## Why\n\nInternal.\n')"
describe 5 "An old-style description" "$(printf '## Summary\n\nDid things.\n')"
: >"$FAKE_GH_LOG"

if ! out="$("$repo/scripts/release/collect-pr-notes.sh" v0.2.0 2>&1)"; then
	fail "collect failed: $out"
fi
expected_en='## v0.2.0

### Crlf (#3)

- What changed in Crlf.

### Scripts (#2)

- What changed in Scripts.

### Old entry

- Old bullet.'
[ "$(section "$repo/CHANGELOG.md" "## v0.2.0")" = "$expected_en" ] || fail "CHANGELOG.md after collecting:
$(cat "$repo/CHANGELOG.md")"
expected_zh='## v0.2.0

### Crlf（中文） (#3)

- Crlf 的变化。

### Scripts（中文） (#2)

- Scripts 的变化。

### 旧条目

- 旧的一条。'
[ "$(section "$repo/CHANGELOG.zh-CN.md" "## v0.2.0")" = "$expected_zh" ] || fail "CHANGELOG.zh-CN.md after collecting:
$(cat "$repo/CHANGELOG.zh-CN.md")"
grep -q '^## Unreleased$' "$repo/CHANGELOG.md" && fail "\"## Unreleased\" is still there"
grep -q '^## 未发布$' "$repo/CHANGELOG.zh-CN.md" && fail "\"## 未发布\" is still there"
grep -q $'\r' "$repo/CHANGELOG.md" && fail "a carriage return reached CHANGELOG.md"
grep -q 'A "### Title"' "$repo/CHANGELOG.md" && fail "the template's comment reached CHANGELOG.md"
grep -q '^## v0.1.0$' "$repo/CHANGELOG.md" || fail "the v0.1.0 section is gone"
case "$out" in *"#5"*"An old-style description"*) ;; *) fail "the pull request without notes is not listed: $out" ;; esac
case "$out" in *"#1 task/before"*) ;; *) fail "the pull request merged before the scripts is not listed: $out" ;; esac
case "$out" in *"#4"*) ;; *) fail "the pull request with None is not mentioned: $out" ;; esac
grep -q 'Before' "$repo/CHANGELOG.md" && fail "the pull request merged before the scripts was copied"
grep -q -- '^pr view 9 ' "$FAKE_GH_LOG" && fail "the release branch's pull request was read"
grep -q -- '^pr view 1 ' "$FAKE_GH_LOG" && fail "the pull request merged before the scripts was read"

# A second run changes nothing.
before="$(sums "$repo")"
out="$("$repo/scripts/release/collect-pr-notes.sh" v0.2.0 2>&1)" || fail "second collect failed: $out"
[ "$(sums "$repo")" = "$before" ] || fail "second collect changed the files:
$(cat "$repo/CHANGELOG.md")"

# A pull request merged after the first run is added on top, once; the version may be given without the v.
(cd "$repo" && merge_pr 6 task/later)
describe 6 "Later" "$(notes_body Later)"
out="$("$repo/scripts/release/collect-pr-notes.sh" 0.2.0 2>&1)" || fail "collect after a later merge failed: $out"
first="$(awk '/^## v0.2.0$/{f=1;next} f && /^### /{print;exit}' "$repo/CHANGELOG.md")"
[ "$first" = "### Later (#6)" ] || fail "the later pull request is not first: $first"
[ "$(grep -c '(#2)$' "$repo/CHANGELOG.md")" = 1 ] || fail "#2 is in CHANGELOG.md more than once"
[ "$(grep -c '(#6)$' "$repo/CHANGELOG.zh-CN.md")" = 1 ] || fail "#6 is not in CHANGELOG.zh-CN.md once"

# A summary written under the version heading stays above the entries.
awk '{print} /^## v0.2.0$/{print "\nThe summary."}' "$repo/CHANGELOG.md" >"$repo/x" && mv "$repo/x" "$repo/CHANGELOG.md"
(cd "$repo" && merge_pr 7 task/after-summary)
describe 7 "After summary" "$(notes_body Summary7)"
"$repo/scripts/release/collect-pr-notes.sh" v0.2.0 >/dev/null 2>&1 || fail "collect after a summary failed"
expected='## v0.2.0

The summary.

### Summary7 (#7)'
[ "$(section "$repo/CHANGELOG.md" "## v0.2.0" | head -n 5)" = "$expected" ] || fail "the summary did not stay on top:
$(cat "$repo/CHANGELOG.md")"

# The release flow on GitHub: the release branch is cut from main and collected, #3 merges into main meanwhile, the
# release branch merges as pull request #4 and v0.2.0 is tagged on that merge. #3 shipped in v0.2.0 without notes, so
# the next release has it (once), together with #5 merged after the tag; #2 stays in v0.2.0 only.
repo="$(new_repo release-flow)"
for n in 2 3 5; do describe "$n" "PR $n" "$(notes_body "Pr$n")"; done
(
	cd "$repo"
	merge_pr 2 task/the-scripts scripts
	g checkout -q -b release-v0.2.0
	scripts/release/collect-pr-notes.sh v0.2.0 >/dev/null 2>&1
	commit "release v0.2.0"
	g checkout -q main
	merge_pr 3 task/during-release
	merge_branch 4 release-v0.2.0
	g tag v0.2.0
	merge_pr 5 task/after-release
	g checkout -q -b release-v0.3.0
)
out="$("$repo/scripts/release/collect-pr-notes.sh" v0.3.0 2>&1)" || fail "collect of the next release failed: $out"
expected='### Pr5 (#5)
### Pr3 (#3)'
[ "$(titles "$repo/CHANGELOG.md" "## v0.3.0")" = "$expected" ] || fail "the next release after a PR merged during the release has:
$(titles "$repo/CHANGELOG.md" "## v0.3.0")"
[ "$(titles "$repo/CHANGELOG.zh-CN.md" "## v0.3.0" | wc -l | tr -d ' ')" = 2 ] || fail "CHANGELOG.zh-CN.md v0.3.0 has:
$(titles "$repo/CHANGELOG.zh-CN.md" "## v0.3.0")"
[ "$(grep -c '(#2)$' "$repo/CHANGELOG.md")" = 1 ] || fail "#2 is not in CHANGELOG.md exactly once"
[ "$(grep -c '(#3)$' "$repo/CHANGELOG.md")" = 1 ] || fail "#3 is not in CHANGELOG.md exactly once"

# A release branch that merges main after collecting and collects again gets #3 (behind the merge's second parent);
# the next release then does not repeat it.
repo="$(new_repo release-merges-main)"
(
	cd "$repo"
	merge_pr 2 task/the-scripts scripts
	g checkout -q -b release-v0.2.0
	scripts/release/collect-pr-notes.sh v0.2.0 >/dev/null 2>&1
	commit "release v0.2.0"
	g checkout -q main
	merge_pr 3 task/during-release
	g checkout -q release-v0.2.0
	now=$((now + 100))
	GIT_AUTHOR_DATE="@$now +0000" GIT_COMMITTER_DATE="@$now +0000" g merge -q --no-ff -m "Merge branch 'main' into release-v0.2.0" main
)
out="$("$repo/scripts/release/collect-pr-notes.sh" v0.2.0 2>&1)" || fail "collect after merging main failed: $out"
expected='### Pr3 (#3)
### Pr2 (#2)
### Old entry'
[ "$(titles "$repo/CHANGELOG.md" "## v0.2.0")" = "$expected" ] || fail "after merging main into the release branch v0.2.0 has:
$(titles "$repo/CHANGELOG.md" "## v0.2.0")"
(
	cd "$repo"
	commit "release v0.2.0 again"
	merge_branch 4 release-v0.2.0
	g tag v0.2.0
	merge_pr 5 task/after-release
	g checkout -q -b release-v0.3.0
)
"$repo/scripts/release/collect-pr-notes.sh" v0.3.0 >/dev/null 2>&1 || fail "the next collect failed"
[ "$(titles "$repo/CHANGELOG.md" "## v0.3.0")" = "### Pr5 (#5)" ] || fail "the next release repeats a PR:
$(titles "$repo/CHANGELOG.md" "## v0.3.0")"

# A release branch that merges main (to resolve a conflict, say) without collecting again: #3 is behind the merge, in
# no version yet, so the next release has it once. The range starts where the release branch was cut, not where it
# last met main.
repo="$(new_repo release-merges-main-no-collect)"
(
	cd "$repo"
	merge_pr 2 task/the-scripts scripts
	g checkout -q -b release-v0.2.0
	scripts/release/collect-pr-notes.sh v0.2.0 >/dev/null 2>&1
	commit "release v0.2.0"
	g checkout -q main
	merge_pr 3 task/during-release
	g checkout -q release-v0.2.0
	now=$((now + 100))
	GIT_AUTHOR_DATE="@$now +0000" GIT_COMMITTER_DATE="@$now +0000" g merge -q --no-ff -m "Merge branch 'main' into release-v0.2.0" main
	merge_branch 4 release-v0.2.0
	g tag v0.2.0
	merge_pr 5 task/after-release
	g checkout -q -b release-v0.3.0
)
out="$("$repo/scripts/release/collect-pr-notes.sh" v0.3.0 2>&1)" || fail "collect after an uncollected merge of main failed: $out"
expected='### Pr5 (#5)
### Pr3 (#3)'
[ "$(titles "$repo/CHANGELOG.md" "## v0.3.0")" = "$expected" ] || fail "after the release branch merged main without collecting, v0.3.0 has:
$(titles "$repo/CHANGELOG.md" "## v0.3.0")"
[ "$(grep -c '(#3)$' "$repo/CHANGELOG.zh-CN.md")" = 1 ] || fail "#3 is not in CHANGELOG.zh-CN.md exactly once"
[ "$(grep -c '(#2)$' "$repo/CHANGELOG.md")" = 1 ] || fail "#2 is not in CHANGELOG.md exactly once"
before="$(sums "$repo")"
"$repo/scripts/release/collect-pr-notes.sh" v0.3.0 >/dev/null 2>&1 || fail "the second collect of v0.3.0 failed"
[ "$(sums "$repo")" = "$before" ] || fail "the second collect of v0.3.0 changed the files"

# A description with "## English" and no "## 中文" is refused by number, and nothing is written.
repo="$(new_repo refuse)"
(cd "$repo" && merge_pr 2 task/the-scripts scripts && merge_pr 3 task/no-chinese)
describe 2 "Good" "$(notes_body Good)"
describe 3 "No Chinese" "$(printf '## English\n\n### No Chinese\n\n- A bullet.\n\n## Why\n\nBecause.\n')"
before="$(sums "$repo")"
if out="$("$repo/scripts/release/collect-pr-notes.sh" v0.2.0 2>&1)"; then
	fail "a description without 中文 was accepted"
fi
case "$out" in *"#3"*中文*) ;; *) fail "the refusal does not name the pull request and the section: $out" ;; esac
[ "$(sums "$repo")" = "$before" ] || fail "a refused collect wrote files"

# Other bad notes are refused too: None on one side only, a part without a "### " title, an empty part.
describe 3 "Half none" "$(printf '## English\n\nNone\n\n## 中文\n\n### 有内容\n\n- 一条。\n')"
out="$("$repo/scripts/release/collect-pr-notes.sh" v0.2.0 2>&1)" && fail "None on one side only was accepted"
describe 3 "No title" "$(printf '## English\n\n- A bullet.\n\n## 中文\n\n### 标题\n\n- 一条。\n')"
out="$("$repo/scripts/release/collect-pr-notes.sh" v0.2.0 2>&1)" && fail "a part without a title was accepted"
case "$out" in *"#3"*English*) ;; *) fail "the missing title is not named: $out" ;; esac
describe 3 "Empty" "$(printf '## English\n\n<!-- hint -->\n\n## 中文\n\n### 标题\n\n- 一条。\n')"
out="$("$repo/scripts/release/collect-pr-notes.sh" v0.2.0 2>&1)" && fail "an empty part was accepted"
# The template left as it is (its placeholder titles) is refused.
describe 3 "Template" "$(cat "$here/../../.github/pull_request_template.md")"
out="$("$repo/scripts/release/collect-pr-notes.sh" v0.2.0 2>&1)" && fail "the template's placeholder was accepted"
case "$out" in *"#3"*placeholder*) ;; *) fail "the placeholder is not named: $out" ;; esac
# A description gh cannot read stops it too.
rm "$FAKE_GH_PRS/3"
out="$("$repo/scripts/release/collect-pr-notes.sh" v0.2.0 2>&1)" && fail "an unreadable description was accepted"
case "$out" in *"#3"*) ;; *) fail "the unreadable description is not named: $out" ;; esac
[ "$(sums "$repo")" = "$before" ] || fail "a refused collect wrote files"
# Not a version.
out="$("$repo/scripts/release/collect-pr-notes.sh" next 2>&1)" && fail "the version \"next\" was accepted"

# Without an "Unreleased" section, the version section is created above the newest version.
repo="$(new_repo create-section)"
awk '/^## Unreleased/{skip=1;next} /^## /{skip=0} !skip' "$repo/CHANGELOG.md" >"$repo/x" && mv "$repo/x" "$repo/CHANGELOG.md"
awk '/^## 未发布/{skip=1;next} /^## /{skip=0} !skip' "$repo/CHANGELOG.zh-CN.md" >"$repo/x" && mv "$repo/x" "$repo/CHANGELOG.zh-CN.md"
(cd "$repo" && commit "no unreleased" && merge_pr 2 task/the-scripts scripts)
describe 2 "One" "$(notes_body One)"
"$repo/scripts/release/collect-pr-notes.sh" v0.2.0 >/dev/null 2>&1 || fail "collect without an Unreleased section failed"
expected='Preamble.

## v0.2.0

### One (#2)

- What changed in One.

## v0.1.0'
[ "$(awk '/^Preamble/,/^## v0.1.0/' "$repo/CHANGELOG.md")" = "$expected" ] || fail "no version section was created:
$(cat "$repo/CHANGELOG.md")"
grep -q '^### One（中文） (#2)$' "$repo/CHANGELOG.zh-CN.md" || fail "no version section was created in CHANGELOG.zh-CN.md"

# ---- notes.sh: Unreleased previews the pull request notes; a tagged version is printed as before, without gh ----

repo="$(new_repo notes)"
(cd "$repo" && merge_pr 2 task/the-scripts scripts)
describe 2 "Pending" "$(notes_body Pending)"
if ! out="$("$repo/scripts/release/notes.sh" Unreleased 2>&1)"; then
	fail "notes.sh Unreleased failed: $out"
fi
expected='### Pending (#2)

- What changed in Pending.

### Old entry

- Old bullet.

---

## 中文

### Pending（中文） (#2)

- Pending 的变化。

### 旧条目

- 旧的一条。'
[ "$out" = "$expected" ] || fail "notes.sh Unreleased printed:
$out"
: >"$FAKE_GH_LOG"
out="$("$repo/scripts/release/notes.sh" v0.1.0 2>&1)" || fail "notes.sh v0.1.0 failed: $out"
expected='First release summary.

### First release

- Released.

---

## 中文

第一版总结。

### 第一版

- 发布了。'
[ "$out" = "$expected" ] || fail "notes.sh v0.1.0 printed:
$out"
[ -s "$FAKE_GH_LOG" ] && fail "notes.sh v0.1.0 called gh: $(cat "$FAKE_GH_LOG")"
# A bad description makes the preview fail instead of leaving it out.
describe 2 "Bad" "$(printf '## English\n\n### Bad\n\n- b\n')"
out="$("$repo/scripts/release/notes.sh" Unreleased 2>&1)" && fail "notes.sh Unreleased passed with a bad description: $out"
case "$out" in *"#2"*) ;; *) fail "notes.sh Unreleased does not name the bad pull request: $out" ;; esac
# The list of pull requests without notes is printed once, not once per language.
describe 2 "Pending" "$(notes_body Pending)"
(cd "$repo" && merge_pr 3 task/no-notes)
describe 3 "No notes here" "$(printf '## Summary\n\nDid things.\n')"
out="$("$repo/scripts/release/notes.sh" Unreleased 2>&1)" || fail "notes.sh Unreleased with a PR without notes failed: $out"
[ "$(printf '%s\n' "$out" | grep -c 'No notes here')" = 1 ] || fail "the PR without notes is not listed exactly once:
$out"

# ---- check-pr-notes.sh ----

repo="$(new_repo check)"
(cd "$repo" && merge_pr 2 task/the-scripts scripts)
base="$(cd "$repo" && git rev-parse HEAD)"
check() { # check BRANCH: runs the check from the base to HEAD as if on BRANCH; prints the output, fails like the check.
	(cd "$repo" && scripts/release/check-pr-notes.sh "$base" HEAD "$1" 2>&1)
}
reset() { (cd "$repo" && g reset -q --hard "$base" && g clean -q -fd); }

# A code-only change passes.
(cd "$repo" && echo code >code.txt && commit code)
out="$(check task/code)" || fail "a code-only PR failed: $out"
reset

# Any edit of CHANGELOG.md fails and points to the pull request description; a release branch passes.
(cd "$repo" && sed 's/^- Released\.$/- Released, with a fix./' CHANGELOG.md >x && mv x CHANGELOG.md && commit changelog)
out="$(check task/changelog)" && fail "a PR editing CHANGELOG.md passed"
case "$out" in *CHANGELOG.md*"pull request description"*) ;; *) fail "the failure does not name the file and the description: $out" ;; esac
out="$(check release-v0.2.0)" || fail "a release branch editing CHANGELOG.md failed: $out"
reset

# The same for CHANGELOG.zh-CN.md.
(cd "$repo" && awk '{print} /^## 未发布/{print "\n### 直接写\n\n- 手写的。"}' CHANGELOG.zh-CN.md >x && mv x CHANGELOG.zh-CN.md && commit changelog-zh)
out="$(check task/changelog-zh)" && fail "a PR editing CHANGELOG.zh-CN.md passed"
case "$out" in *CHANGELOG.zh-CN.md*) ;; *) fail "the failure does not name CHANGELOG.zh-CN.md: $out" ;; esac
reset

# A file added under changes/ fails.
(cd "$repo" && mkdir -p changes && echo '## English' >changes/x.md && commit changes)
out="$(check task/x)" && fail "a PR adding changes/x.md passed"
case "$out" in *changes/x.md*"pull request description"*) ;; *) fail "the failure does not name changes/x.md: $out" ;; esac
reset

# A new top-level entry in MAIN.md's change list fails; a detail under an existing entry and other sections pass.
(cd "$repo" && awk '{print} /^<!-- Change history/{print "- A new entry by hand.\n  - Detail."}' MAIN.md >x && mv x MAIN.md && commit main-entry)
out="$(check task/main-entry)" && fail "a PR adding a MAIN.md entry passed"
case "$out" in *MAIN.md*"pull request description"*) ;; *) fail "the MAIN.md failure does not name the file and the description: $out" ;; esac
out="$(check release-v0.2.0)" || fail "a release branch adding a MAIN.md entry failed: $out"
reset
(cd "$repo" && awk '{print} /^  - Its detail\.$/ && !done {print "  - Another detail."; done=1}' MAIN.md >x && mv x MAIN.md && sed 's/^Text\.$/Other text./' MAIN.md >x && mv x MAIN.md && commit main-detail)
out="$(check task/main-detail)" || fail "a PR adding a detail to a MAIN.md entry failed: $out"
reset
# A new bullet above the marker (a design direction, not a change entry) passes.
(cd "$repo" && awk '/^- An old decision\.$/{print "- A new decision."} {print}' MAIN.md >x && mv x MAIN.md && commit main-decision)
out="$(check task/main-decision)" || fail "a PR adding a bullet above the marker failed: $out"
reset

# Before the base has collect-pr-notes.sh, the check does not apply (the change that adds it folds the old notes into
# the CHANGELOG files).
(cd "$repo" && g rm -q scripts/release/collect-pr-notes.sh && commit old-base)
base="$(cd "$repo" && git rev-parse HEAD)"
(cd "$repo" && cp "$here/collect-pr-notes.sh" scripts/release/ && sed 's/^- Old bullet\.$/- Folded./' CHANGELOG.md >x && mv x CHANGELOG.md && commit fold)
out="$(check task/pr-description-notes)" || fail "the check applied before the base had collect-pr-notes.sh: $out"

if [ "$failures" -gt 0 ]; then
	echo "$failures pull request notes test(s) failed" >&2
	exit 1
fi
echo "pull request notes tests: ok"
