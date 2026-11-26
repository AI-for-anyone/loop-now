#!/usr/bin/env bash
# Release notes: prints the GitHub Release notes for a tag. First it takes this version's section of CHANGELOG.md (a
# level-2 heading whose first word is the tag or the version without v, optionally in brackets, e.g. "## v0.2.0",
# "## [0.2.0] - 2026-11-05", "## v0.3.0-beta.1"), up to the next level-2 heading; failing that, the tag annotation.
# If both are empty (an annotation that only repeats the tag name counts as empty) it fails, so no Release goes out
# with empty notes. English comes first: when CHANGELOG.zh-CN.md has a section for the same version, it is appended
# after the English notes under a "中文" heading; a missing Chinese section is not an error.
#
#   scripts/release/notes.sh <tag>
#   scripts/release/notes.sh Unreleased
#
# "Unreleased" previews the next release before the change notes are collected: the "## Unreleased" and "## 未发布"
# sections with the notes still in changes/ on top, as scripts/release/collect-changes.sh would put them.
#
# The annotation must be read from an annotated tag: the tag actions/checkout fetches is lightweight, so fetch this
# tag once with git fetch before calling.
set -euo pipefail

if [ $# -ne 1 ]; then
	sed -n '2,12p' "$0" >&2
	exit 2
fi
tag="$1"
version="${tag#v}"
root="$(cd "$(dirname "$0")/../.." && pwd)"
collect="$(cd "$(dirname "$0")" && pwd)/collect-changes.sh"

# blank TEXT: true when TEXT has nothing but whitespace. Uses tr rather than ${TEXT//[[:space:]]/}: in bash 3.2 (macOS)
# that substitution takes time roughly cubic in the length, minutes for a 16 KB section.
blank() {
	[ -z "$(printf '%s' "$1" | tr -d '[:space:]')" ]
}

# section FILE: this version's section of a changelog file, without its heading; empty when the file or section is missing.
section() {
	[ -f "$1" ] || return 0
	awk -v tag="$tag" -v version="$version" '
		/^## / {
			if (found) exit
			head = $2
			gsub(/[][]/, "", head)
			if (head == tag || head == version || (tag == "Unreleased" && head == "未发布")) found = 1
			next
		}
		found { print }
	' "$1"
}

# with_pending PART TEXT: for "Unreleased", the notes' PART from changes/ above TEXT; otherwise TEXT.
with_pending() {
	local pending
	if [ "$tag" != Unreleased ]; then
		printf '%s' "$2"
		return
	fi
	pending="$("$collect" --print "$1")" || exit 1
	if blank "$pending"; then
		printf '%s' "$2"
	elif blank "$2"; then
		printf '%s' "$pending"
	else
		printf '%s\n\n%s' "$pending" "$(printf '%s\n' "$2" | sed '/[^[:space:]]/,$!d')"
	fi
}

notes="$(with_pending English "$(section "$root/CHANGELOG.md")")"
if blank "$notes" && [ "$(git -C "$root" cat-file -t "refs/tags/$tag" 2>/dev/null)" = tag ]; then
	notes="$(git -C "$root" for-each-ref "refs/tags/$tag" --format='%(contents:subject)%0a%0a%(contents:body)')"
	[ "$(printf '%s' "$notes" | tr -d '[:space:]')" = "$tag" ] && notes=""
fi
if blank "$notes"; then
	echo "The release notes are empty: add a \"## $tag\" section to CHANGELOG.md (collect the notes in changes/ with scripts/release/collect-changes.sh), or write an annotation with git tag -a $tag." >&2
	exit 1
fi
# Drop leading blank lines (command substitution has already removed the trailing newlines).
printf '%s\n' "$notes" | sed '/[^[:space:]]/,$!d'

# The Chinese supplement, when this version has one.
chinese="$(with_pending 中文 "$(section "$root/CHANGELOG.zh-CN.md")")"
if ! blank "$chinese"; then
	printf '\n---\n\n## 中文\n\n'
	printf '%s\n' "$chinese" | sed '/[^[:space:]]/,$!d'
fi
