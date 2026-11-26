#!/usr/bin/env bash
# Collect change notes: every change records its notes as one file changes/<slug>.md (see changes/README.md), so pull
# requests waiting in the merge queue never edit the same lines. When a release is cut, run this on the release branch
# before renaming "## Unreleased": it puts each note's three parts into place and deletes the note files.
#
#   scripts/release/collect-changes.sh <version>        collect into CHANGELOG.md, CHANGELOG.zh-CN.md and MAIN.md
#   scripts/release/collect-changes.sh --print <part>   print the notes' English, 中文 or MAIN.md part, write nothing
#
# A note has exactly three level-2 sections, in this order: "## English" (a "### Title" and bullets, for the
# "## Unreleased" section of CHANGELOG.md), "## 中文" (the same for "## 未发布" in CHANGELOG.zh-CN.md) and "## MAIN.md"
# (one top-level bullet with its sub-bullets, inserted below the change-entries marker in MAIN.md). Notes are ordered
# by the time git added their file, newest first (a note not committed yet counts as the newest), and go above the
# entries already there, so each section stays newest first. A missing "## Unreleased" or "## 未发布" section is
# created above the newest version. Every note is checked before anything is written: one bad note (naming the file
# and the section) and nothing changes. With no notes left, it changes nothing, so running it twice is safe.
set -euo pipefail

usage() {
	sed -n '2,6p' "$0" >&2
	exit 2
}

root="$(cd "$(dirname "$0")/../.." && pwd)"
marker='<!-- New change entries are inserted below this line by scripts/release/collect-changes.sh, newest first. -->'
sections=("English" "中文" "MAIN.md")

mode=collect
case "${1:-}" in
--print)
	[ $# -eq 2 ] || usage
	mode=print
	print_part="$2"
	case "$print_part" in English | 中文 | MAIN.md) ;; *) usage ;; esac
	;;
'' | -*) usage ;;
*)
	[ $# -eq 1 ] || usage
	version="$1"
	version_pattern='^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'
	if ! [[ "$version" =~ $version_pattern ]]; then
		echo "Not a version: \"$version\" (expected for example v0.3.4 or 0.4.0-beta.1)." >&2
		exit 2
	fi
	;;
esac

# headings FILE: the level-2 headings of a note, one per line, outside code fences.
headings() {
	awk '/^```/ { fence = !fence } !fence && /^## / { print substr($0, 4) }' "$1"
}

# part FILE NAME: the body of section NAME, without leading and trailing blank lines.
part() {
	awk -v want="$2" '
		/^```/ { fence = !fence }
		!fence && /^## / { on = (substr($0, 4) == want); next }
		on { line[++n] = $0; if (NF) { if (!first) first = n; last = n } }
		END { if (first) for (i = first; i <= last; i++) print line[i] }
	' "$1"
}

# before_first_heading FILE: the non-blank lines before the first level-2 heading.
before_first_heading() {
	awk '/^## / { exit } NF { print }' "$1"
}

# check FILE: prints what is wrong with a note, nothing when it is fine.
check() {
	local file="$1" rel="changes/$(basename "$1")" found expected name body first
	found="$(headings "$file")"
	expected="$(printf '%s\n' "${sections[@]}")"
	if [ "$found" != "$expected" ]; then
		for name in "${sections[@]}"; do
			if ! printf '%s\n' "$found" | grep -qxF -- "$name"; then
				echo "$rel: the section \"## $name\" is missing."
				return
			fi
		done
		echo "$rel: expected exactly the sections \"## English\", \"## 中文\" and \"## MAIN.md\" in this order, found: $(printf '%s' "$found" | tr '\n' ',' | sed 's/,/, /g')."
		return
	fi
	if [ -n "$(before_first_heading "$file")" ]; then
		echo "$rel: there is text before \"## English\"."
	fi
	for name in "${sections[@]}"; do
		body="$(part "$file" "$name")"
		if [ -z "$body" ]; then
			echo "$rel: the section \"## $name\" is empty."
			continue
		fi
		first="$(printf '%s\n' "$body" | head -n 1)"
		case "$name" in
		MAIN.md) case "$first" in "- "*) ;; *) echo "$rel: the section \"## MAIN.md\" must start with a top-level bullet (\"- \")." ;; esac ;;
		*) case "$first" in "### "*) ;; *) echo "$rel: the section \"## $name\" must start with a \"### \" title." ;; esac ;;
		esac
	done
}

# The notes, oldest first.
notes=()
keyed=""
for file in "$root"/changes/*.md; do
	[ -e "$file" ] || continue
	case "$(basename "$file")" in README.md | README.*.md) continue ;; esac
	added="$(git -C "$root" log -1 --diff-filter=A --format=%ct -- "changes/$(basename "$file")" 2>/dev/null || true)"
	[ -n "$added" ] || added=9999999999
	keyed="$keyed$(printf '%010d %s' "$added" "$(basename "$file")")"$'\n'
done
while IFS= read -r line; do
	[ -n "$line" ] && notes+=("$root/changes/${line#* }")
done < <(printf '%s' "$keyed" | sort)

errors=""
for file in ${notes[@]+"${notes[@]}"}; do
	errors="$errors$(check "$file")"$'\n'
done
if [ -n "$(printf '%s' "$errors" | tr -d '\n')" ]; then
	printf '%s\n' "$errors" | sed '/^$/d' >&2
	echo "Nothing was collected; fix these notes first." >&2
	exit 1
fi

# joined NAME SEPARATOR: the part NAME of every note, newest first, with SEPARATOR ("blank" or "none") between them.
joined() {
	local i
	for ((i = ${#notes[@]} - 1; i >= 0; i--)); do
		part "${notes[$i]}" "$1"
		if [ "$i" -gt 0 ] && [ "$2" = blank ]; then echo; fi
	done
}

if [ "$mode" = print ]; then
	[ ${#notes[@]} -eq 0 ] || joined "$print_part" "$([ "$print_part" = MAIN.md ] && echo none || echo blank)"
	exit 0
fi

if [ ${#notes[@]} -eq 0 ]; then
	echo "No change notes to collect for $version."
	exit 0
fi
if ! grep -qxF -- "$marker" "$root/MAIN.md"; then
	echo "MAIN.md has no change-entries marker; put this line where new entries go, then run again:" >&2
	echo "$marker" >&2
	exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# into_section FILE HEADING TEXT_FILE OUT: FILE with TEXT_FILE put at the top of the section HEADING (created above the
# first level-2 heading when missing), one blank line around it.
into_section() {
	local has=0
	grep -qxF -- "$2" "$1" && has=1
	awk -v h="$2" -v f="$3" -v has="$has" '
		function insert(  l) { while ((getline l < f) > 0) print l; close(f) }
		!done && has && $0 == h { print; print ""; insert(); skip = 1; done = 1; next }
		!done && !has && /^## / { print h; print ""; insert(); print ""; done = 1; print; next }
		skip { if ($0 !~ /[^[:space:]]/) next; print ""; skip = 0 }
		{ print }
		END { if (!done) { print ""; print h; print ""; insert() } }
	' "$1" >"$4"
}

joined English blank >"$tmp/english"
joined 中文 blank >"$tmp/chinese"
joined MAIN.md none >"$tmp/main"
into_section "$root/CHANGELOG.md" "## Unreleased" "$tmp/english" "$tmp/CHANGELOG.md"
into_section "$root/CHANGELOG.zh-CN.md" "## 未发布" "$tmp/chinese" "$tmp/CHANGELOG.zh-CN.md"
awk -v m="$marker" -v f="$tmp/main" '
	{ print }
	$0 == m { while ((getline l < f) > 0) print l; close(f) }
' "$root/MAIN.md" >"$tmp/MAIN.md"

cat "$tmp/CHANGELOG.md" >"$root/CHANGELOG.md"
cat "$tmp/CHANGELOG.zh-CN.md" >"$root/CHANGELOG.zh-CN.md"
cat "$tmp/MAIN.md" >"$root/MAIN.md"
for file in "${notes[@]}"; do
	rm "$file"
done

echo "Collected ${#notes[@]} change note(s) for $version, newest first:"
for ((i = ${#notes[@]} - 1; i >= 0; i--)); do
	echo "  changes/$(basename "${notes[$i]}")"
done
echo "Next: rename \"## Unreleased\" and \"## 未发布\" to \"## $version\", add the summary and the \"Release:\" lines, and commit."
