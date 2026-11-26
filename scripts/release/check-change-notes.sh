#!/usr/bin/env bash
# Change notes check (CI, on pull requests): a change records its notes as one new file changes/<slug>.md (see
# changes/README.md), never by editing the shared places directly, because pull requests waiting in the merge queue
# that edit the same lines conflict and are dropped. Fails when the change from BASE to HEAD
#   - adds a non-blank line inside the "## Unreleased" section of CHANGELOG.md or "## 未发布" of CHANGELOG.zh-CN.md, or
#   - adds a top-level entry ("- " at the start of a line) to MAIN.md's change list, the level-2 section that holds the
#     change-entries marker (new details under an existing entry, edits and removals are fine).
# Passes on a release branch (release-*), where scripts/release/collect-changes.sh fills those places, and when BASE
# has no collect-changes.sh yet (the change that adds it still writes its notes the old way).
#
#   scripts/release/check-change-notes.sh <base commit> <head commit> <branch name>
#
# In CI on a pull request, HEAD is the merge of the pull request into its base, so the base is HEAD^1.
set -euo pipefail

if [ $# -ne 3 ]; then
	sed -n '2,13p' "$0" >&2
	exit 2
fi
base="$1"
head="$2"
branch="$3"
marker='<!-- New change entries are inserted below this line by scripts/release/collect-changes.sh, newest first. -->'

case "$branch" in
release-*)
	echo "Change notes: $branch is a release branch, where the notes are collected; not checked."
	exit 0
	;;
esac
if ! git cat-file -e "$base:scripts/release/collect-changes.sh" 2>/dev/null; then
	echo "Change notes: the base has no scripts/release/collect-changes.sh yet; not checked."
	exit 0
fi

slug="${branch#task/}"
[ -n "$slug" ] || slug="<slug>"
problems=0

# section_range FILE HEADING: "first last" line numbers of the section HEADING in FILE at HEAD, or nothing.
section_range() {
	git show "$head:$1" 2>/dev/null | awk -v h="$2" '
		$0 == h { start = NR; next }
		start && /^## / { print start, NR - 1; found = 1; exit }
		END { if (start && !found) print start, NR }
	'
}

# added_lines FILE: the line numbers at HEAD of the non-blank lines the change adds to FILE.
added_lines() {
	git diff --no-ext-diff --no-color -U0 "$base" "$head" -- "$1" | awk '
		/^@@ / {
			split($3, plus, ",")
			n = substr(plus[1], 2) + 0
			next
		}
		/^\+\+\+ / { next }
		/^\+/ { if (substr($0, 2) ~ /[^[:space:]]/) print n; n++; next }
		/^ / { n++ }
	'
}

for pair in "CHANGELOG.md|## Unreleased|English" "CHANGELOG.zh-CN.md|## 未发布|中文"; do
	file="${pair%%|*}"
	rest="${pair#*|}"
	heading="${rest%%|*}"
	part="${rest#*|}"
	range="$(section_range "$file" "$heading")"
	[ -n "$range" ] || continue
	first="${range% *}"
	last="${range#* }"
	lines="$(added_lines "$file" | awk -v a="$first" -v b="$last" '$1 >= a && $1 <= b' | tr '\n' ' ' | sed 's/ $//')"
	if [ -n "$lines" ]; then
		echo "$file: this change adds lines under \"$heading\" (line $(echo "$lines" | sed 's/ /, /g')). Write the entry in a change note instead: the \"## $part\" section of changes/$slug.md (see changes/README.md)."
		problems=$((problems + 1))
	fi
done

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
	echo "MAIN.md: this change adds $((after - before)) new entr$([ $((after - before)) -eq 1 ] && echo y || echo ies) to the change list. Write the entry in a change note instead: the \"## MAIN.md\" section of changes/$slug.md (see changes/README.md)."
	problems=$((problems + 1))
fi

if [ "$problems" -gt 0 ]; then
	exit 1
fi
echo "Change notes: ok."
