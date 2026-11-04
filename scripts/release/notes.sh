#!/usr/bin/env bash
# 发布说明：输出 tag 对应的 GitHub Release 说明。先取 CHANGELOG.md 里这个版本的一节（二级标题的第一个词是
# tag 或去掉 v 的版本号，可带方括号，例如 "## v0.2.0"、"## [0.2.0] - 2026-11-05"），到下一个二级标题为止；
# 没有就取 tag 的注解。两处都没有内容（注解只写了 tag 名也算没有）时失败，不发空说明的 Release。
#
#   scripts/release/notes.sh <tag>
#
# 注解要从附注 tag 读：actions/checkout 拉下来的 tag 是轻量的，调用前先 git fetch 一次这个 tag。
set -euo pipefail

if [ $# -ne 1 ]; then
	sed -n '2,8p' "$0" >&2
	exit 2
fi
tag="$1"
version="${tag#v}"
root="$(cd "$(dirname "$0")/../.." && pwd)"

notes=""
if [ -f "$root/CHANGELOG.md" ]; then
	notes="$(awk -v tag="$tag" -v version="$version" '
		/^## / {
			if (found) exit
			head = $2
			gsub(/[][]/, "", head)
			if (head == tag || head == version) found = 1
			next
		}
		found { print }
	' "$root/CHANGELOG.md")"
fi
if [ -z "${notes//[[:space:]]/}" ] && [ "$(git -C "$root" cat-file -t "refs/tags/$tag" 2>/dev/null)" = tag ]; then
	notes="$(git -C "$root" for-each-ref "refs/tags/$tag" --format='%(contents:subject)%0a%0a%(contents:body)')"
	[ "$(printf '%s' "$notes" | tr -d '[:space:]')" = "$tag" ] && notes=""
fi
if [ -z "${notes//[[:space:]]/}" ]; then
	echo "发布说明为空：在 CHANGELOG.md 加一节 \"## $tag\"，或用 git tag -a $tag 写上注解" >&2
	exit 1
fi
# 去掉开头的空行（结尾的换行已被命令替换去掉）。
printf '%s\n' "$notes" | sed '/[^[:space:]]/,$!d'
