#!/usr/bin/env bash
# Pull request notes check (CI, on pull requests): a change records its notes in its pull request description, under
# "## English" and "## 中文" (see .github/pull_request_template.md), never in shared files that every change would
# edit, because pull requests waiting in the merge queue that edit the same lines conflict and are dropped. Fails when
# the change from BASE to HEAD
#   - changes CHANGELOG.md or CHANGELOG.zh-CN.md (the release writes them with scripts/release/collect-pr-notes.sh),
#   - adds a file under changes/ (the old note files), or
#   - adds a top-level entry ("- " at the start of a line) to MAIN.md's change list, the level-2 section that holds the
#     change-history marker line (new details under an existing entry, edits and removals are fine).
# Passes on a release branch (release-*), where the CHANGELOG files are written, and when BASE has no
# collect-pr-notes.sh yet (the change that adds it folds the last note files into the CHANGELOG files).
#
#   scripts/release/check-pr-notes.sh <base commit> <head commit> <branch name>
#
# In CI on a pull request, HEAD is the merge of the pull request into its base, so the base is HEAD^1.
set -euo pipefail

if [ $# -ne 3 ]; then
	sed -n '2,15p' "$0" >&2
	exit 2
fi
base="$1"
head="$2"
branch="$3"
marker='<!-- Change history lives in the pull requests and CHANGELOG.md, not here: do not add entries for changes below this line. -->'
where='Put the notes in the pull request description instead, under "## English" and "## 中文" (see .github/pull_request_template.md); the release writes the CHANGELOG files from them.'

case "$branch" in
release-*)
	echo "Pull request notes: $branch is a release branch, where the CHANGELOG files are written; not checked."
	exit 0
	;;
esac
if ! git cat-file -e "$base:scripts/release/collect-pr-notes.sh" 2>/dev/null; then
	echo "Pull request notes: the base has no scripts/release/collect-pr-notes.sh yet; not checked."
	exit 0
fi

problems=0

while IFS= read -r file; do
	[ -n "$file" ] || continue
	echo "$file: a pull request does not edit the CHANGELOG files. $where"
	problems=$((problems + 1))
done < <(git diff --no-ext-diff --name-only "$base" "$head" -- CHANGELOG.md CHANGELOG.zh-CN.md)

while IFS= read -r file; do
	[ -n "$file" ] || continue
	echo "$file: change note files are gone. $where"
	problems=$((problems + 1))
done < <(git diff --no-ext-diff --name-only --diff-filter=A "$base" "$head" -- changes/)

# entries REV: the number of top-level entries in the section of MAIN.md that holds the marker, at REV.
entries() {
	git show "$1:MAIN.md" 2>/dev/null | awk -v m="$marker" '
		/^## / { if (in_list) exit; count = 0 }
		$0 == m { in_list = 1 }
		/^- / { count++ }
		END { if (in_list) print count }
	'
}
before="$(entries "$base")"
after="$(entries "$head")"
if [ -n "$before" ] && [ -n "$after" ] && [ "$after" -gt "$before" ]; then
	echo "MAIN.md: this change adds $((after - before)) new entr$([ $((after - before)) -eq 1 ] && echo y || echo ies) to the change list, which no longer grows. $where"
	problems=$((problems + 1))
fi

if [ "$problems" -gt 0 ]; then
	exit 1
fi
echo "Pull request notes: ok."
