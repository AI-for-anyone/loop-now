#!/usr/bin/env bash
# Collect pull request notes: a change records its notes in its pull request description, under "## English" (a
# "### Title" and bullets, as they will appear in CHANGELOG.md) and "## 中文" (the same for CHANGELOG.zh-CN.md); see
# .github/pull_request_template.md. A pull request never edits the CHANGELOG files, so pull requests waiting in the
# merge queue do not conflict there. When a release is cut, run this on the release branch:
#
#   scripts/release/collect-pr-notes.sh <version>               write the "## v<version>" sections of both files
#   scripts/release/collect-pr-notes.sh --print English|中文    print the notes that are not released yet, write nothing
#
# It reads the pull requests merged since the previous release: the "Merge pull request #N" commits reachable from HEAD
# (main merges through the merge queue with merge commits; a release branch that merged main sees those behind the
# merge too), each description read with "gh pr view N" (gh is used here, in the release process only, never by the
# product). The previous release is the newest v* tag reachable from HEAD; when that tag is on the merge of a release-*
# pull request, the range starts where that release branch was cut from main, so a pull request merged into main while
# the release was being prepared comes into the next release, unless the release branch merged main and collected it. Each pull request's notes
# get its number after their titles ("### Title (#N)"); a number already in a title anywhere in the file is not copied
# again, so running it twice, or a range that reaches back into the previous release, adds nothing twice. The notes go
# newest first into the version section, above the entries already there (a "## Unreleased" / "## 未发布" section
# becomes the version section) and below a summary paragraph written under the heading.
# Not copied, and listed with number and title so the release session can decide: pull requests whose description has
# neither section, and pull requests merged before this script was on main (their notes reached the CHANGELOG through
# the old changes/ files or by hand). "None" / "无" in both sections means nobody using Loop Now can see the change: it
# is skipped. Release branches (release-*) are skipped. Every description is checked before anything is written: one
# bad description (only one of the two sections, None on one side only, a part that does not start with a "### "
# title or still has the template's placeholder title, an empty part, or one gh cannot read) is named by its number and
# nothing changes.
set -euo pipefail

usage() {
	sed -n '7,8p' "$0" >&2
	exit 2
}

root="$(cd "$(dirname "$0")/../.." && pwd)"
self="scripts/release/collect-pr-notes.sh"

mode=collect
case "${1:-}" in
--print)
	[ $# -eq 2 ] || usage
	mode=print
	print_part="$2"
	case "$print_part" in English | 中文) ;; *) usage ;; esac
	;;
'' | -*) usage ;;
*)
	[ $# -eq 1 ] || usage
	version_pattern='^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'
	if ! [[ "$1" =~ $version_pattern ]]; then
		echo "Not a version: \"$1\" (expected for example v0.3.4 or 0.4.0-beta.1)." >&2
		exit 2
	fi
	version="v${1#v}"
	;;
esac

if ! command -v gh >/dev/null 2>&1; then
	echo "gh is not installed; it reads the pull request descriptions (https://cli.github.com)." >&2
	exit 1
fi

exclude=()
[ "$mode" = collect ] && exclude=(--exclude "$version")
if ! previous="$(git -C "$root" describe --tags --abbrev=0 --match 'v[0-9]*' ${exclude[@]+"${exclude[@]}"} HEAD 2>/dev/null)"; then
	echo "No previous release tag (v*) is reachable from HEAD; fetch the tags (git fetch --tags) and run again." >&2
	exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# The range starts at the previous tag, or, when the tag is on the merge of a release branch, where that branch was cut
# from main: the first commit of the branch's first-parent chain that is also on main's. Not the merge-base: a release
# branch that merged main meets it later, and the pull requests merged into main while it was prepared are not in that
# release's notes unless it collected again (then their numbers are in the file and are not copied twice).
lower="$previous"
subject="$(git -C "$root" log -1 --format=%s "$previous")"
if [[ "$subject" =~ ^Merge\ pull\ request\ \#[0-9]+\ from\ ([^ ]+) ]] &&
	case "${BASH_REMATCH[1]#*/}" in release-*) true ;; *) false ;; esac &&
	git -C "$root" rev-parse -q --verify "$previous^2" >/dev/null; then
	git -C "$root" rev-list --first-parent "$previous^1" >"$tmp/main-chain"
	cut="$(git -C "$root" rev-list --first-parent "$previous^2" | grep -Fx -f "$tmp/main-chain" | head -n 1 || true)"
	[ -n "$cut" ] || cut="$(git -C "$root" merge-base "$previous^1" "$previous^2")"
	lower="$cut"
fi

# part FILE NAME: the body of the level-2 section NAME of a description, outside code fences, without HTML comments and
# without leading and trailing blank lines.
part() {
	awk -v want="$2" '
		/^```/ { fence = !fence }
		!fence && /^## / { h = substr($0, 4); sub(/[ \t]+$/, "", h); on = (h == want); next }
		!on { next }
		{
			out = ""; rest = $0; was = incomment
			while (rest != "") {
				if (incomment) {
					i = index(rest, "-->")
					if (!i) { rest = ""; break }
					rest = substr(rest, i + 3); incomment = 0
				} else {
					i = index(rest, "<!--")
					if (!i) { out = out rest; rest = ""; break }
					out = out substr(rest, 1, i - 1); rest = substr(rest, i + 4); incomment = 1
				}
			}
			if (out !~ /[^ \t]/ && (was || $0 ~ /<!--/)) next
			line[++n] = out
			if (out ~ /[^ \t]/) { if (!first) first = n; last = n }
		}
		END { if (first) for (i = first; i <= last; i++) print line[i] }
	' "$1"
}

# has_section FILE NAME: true when the description has the level-2 heading NAME outside code fences.
has_section() {
	awk -v want="$2" '
		/^```/ { fence = !fence }
		!fence && /^## / { h = substr($0, 4); sub(/[ \t]+$/, "", h); if (h == want) { found = 1; exit } }
		END { exit !found }
	' "$1"
}

# is_none TEXT: true when TEXT says the change is not visible ("None" or "无", optionally with a full stop).
is_none() {
	case "$(printf '%s' "$1" | tr -d '[:space:]')" in None | none | NONE | None. | 无 | 无。) return 0 ;; *) return 1 ;; esac
}

# titled TEXT N: TEXT with " (#N)" after every "### " title.
titled() {
	printf '%s\n' "$1" | awk -v n="$2" '/^### / { sub(/[ \t]+$/, ""); print $0 " (#" n ")"; next } { print }'
}

# The pull requests, newest first: "number<TAB>merge commit".
prs=()
while IFS= read -r line; do
	commit="${line%% *}"
	subject="${line#* }"
	[[ "$subject" =~ ^Merge\ pull\ request\ \#([0-9]+)\ from\ ([^ ]+) ]] || continue
	number="${BASH_REMATCH[1]}"
	branch="${BASH_REMATCH[2]}"
	case "${branch#*/}" in release-*) continue ;; esac
	prs+=("$number	$commit")
done < <(git -C "$root" log --merges --date-order --format='%H %s' "$lower..HEAD")

errors=""
listed=""
before=""
skipped=""
kept=() # numbers whose notes are copied, newest first
for entry in ${prs[@]+"${prs[@]}"}; do
	number="${entry%%	*}"
	commit="${entry#*	}"
	if ! git -C "$root" cat-file -e "$commit:$self" 2>/dev/null; then
		before="$before  #$number $(git -C "$root" log -1 --format=%b "$commit" | head -n 1)"$'\n'
		continue
	fi
	if ! gh pr view "$number" --json title,body --jq '.title + "\n" + (.body // "")' >"$tmp/raw" 2>"$tmp/err"; then
		errors="$errors#$number: gh could not read the description: $(tr '\n' ' ' <"$tmp/err")"$'\n'
		continue
	fi
	tr -d '\r' <"$tmp/raw" >"$tmp/pr-$number"
	title="$(head -n 1 "$tmp/pr-$number")"
	tail -n +2 "$tmp/pr-$number" >"$tmp/body-$number"
	body="$tmp/body-$number"
	has_en=0
	has_zh=0
	has_section "$body" English && has_en=1
	has_section "$body" 中文 && has_zh=1
	if [ "$has_en" = 0 ] && [ "$has_zh" = 0 ]; then
		listed="$listed  #$number $title"$'\n'
		continue
	fi
	if [ "$has_en" = 0 ] || [ "$has_zh" = 0 ]; then
		errors="$errors#$number ($title): the description has \"## $([ "$has_en" = 1 ] && echo English || echo 中文)\" but no \"## $([ "$has_en" = 1 ] && echo 中文 || echo English)\"; write both, or \"None\" / \"无\" in both."$'\n'
		continue
	fi
	en="$(part "$body" English)"
	zh="$(part "$body" 中文)"
	if is_none "$en" && is_none "$zh"; then
		skipped="$skipped  #$number $title"$'\n'
		continue
	fi
	bad=0
	for name in English 中文; do
		text="$en"
		[ "$name" = 中文 ] && text="$zh"
		if [ -z "$text" ]; then
			errors="$errors#$number ($title): the section \"## $name\" is empty; write the notes, or \"None\" / \"无\" in both sections."$'\n'
			bad=1
		elif is_none "$text"; then
			errors="$errors#$number ($title): \"## $name\" says None but the other section has notes; write both or neither."$'\n'
			bad=1
		else
			case "$(printf '%s\n' "$text" | head -n 1 | sed 's/[[:space:]]*$//')" in
			"### Title" | "### 标题")
				errors="$errors#$number ($title): the section \"## $name\" still has the template's placeholder title; write the notes, or \"None\" / \"无\" in both sections."$'\n'
				bad=1
				;;
			"### "*) ;;
			*)
				errors="$errors#$number ($title): the section \"## $name\" must start with a \"### \" title."$'\n'
				bad=1
				;;
			esac
		fi
	done
	[ "$bad" = 0 ] || continue
	titled "$en" "$number" >"$tmp/en-$number"
	titled "$zh" "$number" >"$tmp/zh-$number"
	kept+=("$number")
done

if [ -n "$errors" ]; then
	printf '%s' "$errors" >&2
	echo "Nothing was written; fix these pull request descriptions on GitHub first." >&2
	exit 1
fi

# report: what was not copied, on stderr (so --print stays clean).
report() {
	if [ -n "$listed" ]; then
		echo "No \"## English\" / \"## 中文\" in the description, not copied; decide whether they need an entry:" >&2
		printf '%s' "$listed" >&2
	fi
	if [ -n "$before" ]; then
		echo "Merged before $self was on main, not copied (their notes reached the CHANGELOG through changes/ or by hand):" >&2
		printf '%s' "$before" >&2
	fi
	if [ -n "$skipped" ]; then
		echo "None / 无 (nobody using Loop Now can see it), skipped:" >&2
		printf '%s' "$skipped" >&2
	fi
}

# joined LANG FILE: the notes of the kept pull requests in LANG (en or zh), newest first, a blank line between them,
# leaving out those whose number is already in a title of FILE.
joined() {
	local number sep=""
	for number in ${kept[@]+"${kept[@]}"}; do
		if in_file "$2" "$number"; then
			continue
		fi
		printf '%s' "$sep"
		cat "$tmp/$1-$number"
		sep=$'\n'
	done
}

# in_file FILE N: true when a "### ... (#N)" title is in FILE.
in_file() {
	awk -v t="(#$2)" '
		/^### / && substr($0, length($0) - length(t) + 1) == t { found = 1; exit }
		END { exit !found }
	' "$1"
}

if [ "$mode" = print ]; then
	report
	if [ "$print_part" = English ]; then
		joined en "$root/CHANGELOG.md"
	else
		joined zh "$root/CHANGELOG.zh-CN.md"
	fi
	exit 0
fi

# into_version FILE UNRELEASED TEXT OUT: FILE with the section "## <version>" holding TEXT above its first "### " entry
# (below a summary paragraph). The section is "## <version>" if there is one, else the UNRELEASED section renamed, else
# a new one above the first level-2 heading. An empty TEXT only renames.
into_version() {
	local mode=new
	if grep -qxF -- "## $version" "$1"; then
		mode=version
	elif grep -qxF -- "$2" "$1"; then
		mode=unreleased
	fi
	awk -v vh="## $version" -v uh="$2" -v f="$3" -v mode="$mode" '
		function insert(  l, any) {
			while ((getline l < f) > 0) {
				if (!any && last ~ /[^[:space:]]/) emit("")
				emit(l); any = 1
			}
			close(f)
			if (any) emit("")
			done = 1
		}
		function emit(s) { print s; last = s }
		!done && mode == "new" && /^## / { emit(vh); emit(""); insert(); emit($0); next }
		!done && !on && ((mode == "version" && $0 == vh) || (mode == "unreleased" && $0 == uh)) { emit(vh); on = 1; next }
		!done && on && (/^### / || /^## /) { insert(); emit($0); next }
		{ emit($0) }
		END {
			if (!done && (on || mode == "new")) {
				if (mode == "new") { if (last ~ /[^[:space:]]/) emit(""); emit(vh) }
				insert()
			}
		}
	' "$1" >"$4"
}

report
joined en "$root/CHANGELOG.md" >"$tmp/english"
joined zh "$root/CHANGELOG.zh-CN.md" >"$tmp/chinese"
for pair in "CHANGELOG.md|## Unreleased|english" "CHANGELOG.zh-CN.md|## 未发布|chinese"; do
	file="${pair%%|*}"
	rest="${pair#*|}"
	if [ ! -s "$tmp/${rest#*|}" ] && ! grep -qxF -- "${rest%%|*}" "$root/$file"; then
		cp "$root/$file" "$tmp/out-$file"
		continue
	fi
	into_version "$root/$file" "${rest%%|*}" "$tmp/${rest#*|}" "$tmp/out-$file"
done
added="$(grep -c '^### ' "$tmp/english" || true)"
cat "$tmp/out-CHANGELOG.md" >"$root/CHANGELOG.md"
cat "$tmp/out-CHANGELOG.zh-CN.md" >"$root/CHANGELOG.zh-CN.md"

echo "Pull requests since $previous$([ "$lower" = "$previous" ] || echo " (from where its release branch was cut)"): ${#prs[@]}; notes of ${#kept[@]} in \"## $version\" ($added new entr$([ "$added" = 1 ] && echo y || echo ies) this run)."
echo "Next: write the summary paragraph under \"## $version\" in CHANGELOG.md and CHANGELOG.zh-CN.md, check with scripts/release/notes.sh $version, and commit."
