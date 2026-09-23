#!/bin/sh
# Turn the assembled feed into a browsable Jekyll site.
#
#   generate-feed-pages.sh <site-dir> <base-url> [target-map]
#
# The packages stay where the feed job put them, at
# <site>/releases/<release>/<pkgarch>/. What this adds is a tree keyed the way
# a user thinks -- release, target, subtarget -- whose leaves point at the
# right pkgarch feed. Targets get no copy of the packages: five architectures
# serve 38 targets, so copying would multiply the site for nothing.

# The file is printf templates full of Markdown backticks, which shellcheck
# reads as command substitution it thinks should have been double-quoted.
# shellcheck disable=SC2016

set -eu

SITE=${1:?site directory is required}
BASE=${2:?base URL is required}
MAP=${3:-.github/openwrt-targets.json}

command -v jq >/dev/null 2>&1 || { echo "jq is required"; exit 1; }

# jq's Windows build emits CRLF, which silently breaks every match below. The
# runner never sees it; anyone testing this locally would.
CR=$(printf '\r')
jqr() { jq -r "$@" | tr -d "$CR"; }

# opkg finds a key by its id, so the snippet must name the file for it. A usign
# public key is a 2-byte algorithm tag then the 8-byte id.
KEYID=$(sed -n '2p' keys/qwdtt-usign.pub | base64 -d | od -An -tx1 -j2 -N8 | tr -d ' \n')

mkdir -p "$SITE/assets"
cp assets/copy-code.js assets/copy-code.css "$SITE/assets/"

# `repository` is not decoration: the github-pages gem refuses to build
# without it, and the error names neither the key nor the file.
cat > "$SITE/_config.yml" <<EOF
title: qWDTT OpenWrt Feed
description: Signed package feed for qWDTT on OpenWrt
theme: jekyll-theme-midnight
repository: $GITHUB_REPOSITORY
EOF

# The theme's layout has no hook for extra assets, so each page pulls the copy
# button in for itself.
foot() {
	printf '\n<link rel="stylesheet" href="%s/assets/copy-code.css">\n' "$BASE"
	printf '<script src="%s/assets/copy-code.js"></script>\n' "$BASE"
}

head_() { # head_ <file> <title>
	mkdir -p "$(dirname "$1")"
	printf -- '---\nlayout: default\ntitle: "%s"\n---\n' "$2" > "$1"
}

# ------------------------------------------------------------------ root ----
head_ "$SITE/index.md" "qWDTT OpenWrt Feed"
{
	printf '\n# qWDTT OpenWrt Feed\n\n'
	printf 'A signed package feed for [qWDTT](https://github.com/%s) on OpenWrt.\n' "$GITHUB_REPOSITORY"
	printf 'Pick your OpenWrt release, then your target, and that page gives the\n'
	printf 'exact commands for your router.\n\n'
	printf 'With SSH, the one-line installer does all of it for you:\n\n'
	printf '```sh\nwget -qO- https://raw.githubusercontent.com/%s/main/install.sh | sh\n```\n' "$GITHUB_REPOSITORY"
	printf '\n## OpenWrt releases\n\n'
} >> "$SITE/index.md"

for reldir in "$SITE"/releases/*/; do
	[ -d "$reldir" ] || continue
	rel=$(basename "$reldir")
	jq -e --arg r "$rel" 'has($r)' "$MAP" >/dev/null 2>&1 || continue

	relver=$(jqr --arg r "$rel" '.[$r].version' "$MAP")
	case "$rel" in
	24.10) fmt=opkg ;;
	*) fmt=apk ;;
	esac

	# A feed directory is one carrying an index. Computed before any target
	# directory is written below, so the two cannot be confused.
	published=$(for d in "$reldir"*/; do
		[ -f "$d/Packages" ] || [ -f "$d/packages.adb" ] || continue
		b=${d%/}
		echo "${b##*/}"
	done)

	# `grep -q ... && echo` would leave the loop's exit status at grep's, and
	# a final miss then aborts the script under set -e.
	served=$(jqr --arg r "$rel" '.[$r].targets | to_entries[] | "\(.key) \(.value)"' "$MAP" |
		while read -r path arch; do
			if echo "$published" | grep -qx "$arch"; then
				echo "$path $arch"
			fi
		done)
	[ -n "$served" ] || continue

	printf -- '- [OpenWrt %s](%s/releases/%s/) -- `%s` packages, %s targets\n' \
		"$rel" "$BASE" "$rel" "$fmt" "$(echo "$served" | wc -l)" >> "$SITE/index.md"

	# --------------------------------------------------------- release ------
	head_ "$reldir/index.md" "OpenWrt $rel"
	{
		printf '\n# OpenWrt %s\n\n' "$rel"
		printf 'Index of [(root)](%s/)\n\n' "$BASE"
		printf 'Built against OpenWrt %s, `%s` packages. Choose your target.\n' "$relver" "$fmt"
		printf '\n## Targets\n\n'
		echo "$served" | cut -d' ' -f1 | cut -d/ -f1 | sort -u | while read -r t; do
			printf -- '- [%s](%s/releases/%s/%s/)\n' "$t" "$BASE" "$rel" "$t"
		done
		foot
	} >> "$reldir/index.md"

	# --------------------------------------------------------- targets ------
	echo "$served" | cut -d' ' -f1 | cut -d/ -f1 | sort -u | while read -r target; do
		head_ "$reldir/$target/index.md" "OpenWrt $rel $target"
		{
			printf '\n# %s\n\n' "$target"
			printf 'Index of [(root)](%s/) / [%s](%s/releases/%s/)\n' "$BASE" "$rel" "$BASE" "$rel"
			printf '\n## Subtargets\n\n'
			echo "$served" | while read -r path arch; do
				case "$path" in "$target"/*) ;; *) continue ;; esac
				printf -- '- [%s](%s/releases/%s/%s/) -- `%s`\n' \
					"${path#*/}" "$BASE" "$rel" "$path" "$arch"
			done
			foot
		} >> "$reldir/$target/index.md"
	done

	# ------------------------------------------------------------ leaves ----
	echo "$served" | while read -r path arch; do
		target=${path%%/*}
		feed="$BASE/releases/$rel/$arch"

		head_ "$reldir/$path/index.md" "OpenWrt $rel $path"
		{
			printf '\n# qWDTT for %s\n\n' "$path"
			printf 'Index of [(root)](%s/) / [%s](%s/releases/%s/) / [%s](%s/releases/%s/%s/)\n\n' \
				"$BASE" "$rel" "$BASE" "$rel" "$target" "$BASE" "$rel" "$target"
			printf -- '- OpenWrt release: `%s`\n' "$relver"
			printf -- '- Target: `%s`\n' "$path"
			printf -- '- Package architecture: `%s`\n' "$arch"
			printf -- '- Upstream: [downloads.openwrt.org](https://downloads.openwrt.org/releases/%s/targets/%s/)\n' \
				"$relver" "$path"

			printf '\n## Add the feed\n\n```sh\n'
			if [ "$fmt" = apk ]; then
				printf 'mkdir -p /etc/apk/keys /etc/apk/repositories.d\n'
				printf 'wget -O /etc/apk/keys/qwdtt.pem %s/qwdtt.pem\n' "$BASE"
				printf 'echo "%s/packages.adb" > /etc/apk/repositories.d/qwdtt.list\n' "$feed"
				printf 'apk update\n```\n'
				printf '\n## Install\n\n```sh\napk add qwdtt-client luci-proto-qwdtt\n```\n'
			else
				printf 'mkdir -p /etc/opkg/keys\n'
				printf 'wget -O /etc/opkg/keys/%s %s/qwdtt-usign.pub\n' "$KEYID" "$BASE"
				printf 'echo "src/gz qwdtt %s" > /etc/opkg/qwdtt.conf\n' "$feed"
				printf 'opkg update\n```\n'
				printf '\n## Install\n\n```sh\nopkg install qwdtt-client luci-proto-qwdtt\n```\n'
			fi

			printf '\n## Feed files\n\n'
			for f in "$reldir$arch"/*; do
				[ -f "$f" ] || continue
				b=$(basename "$f")
				case "$b" in index.md) continue ;; esac
				printf -- '- [%s](%s/%s)\n' "$b" "$feed" "$b"
			done
			foot
		} >> "$reldir/$path/index.md"
	done

	# ------------------------------------------------- one page per arch ----
	echo "$published" | while read -r arch; do
		[ -n "$arch" ] || continue
		head_ "$reldir$arch/index.md" "OpenWrt $rel $arch"
		{
			printf '\n# %s\n\n' "$arch"
			printf 'Index of [(root)](%s/) / [%s](%s/releases/%s/)\n' "$BASE" "$rel" "$BASE" "$rel"
			printf '\nServes these targets:\n\n'
			echo "$served" | while read -r path a; do
				[ "$a" = "$arch" ] || continue
				printf -- '- [%s](%s/releases/%s/%s/)\n' "$path" "$BASE" "$rel" "$path"
			done
			printf '\n## Feed files\n\n'
			for f in "$reldir$arch"/*; do
				[ -f "$f" ] || continue
				b=$(basename "$f")
				case "$b" in index.md) continue ;; esac
				printf -- '- [%s](%s/releases/%s/%s/%s)\n' "$b" "$BASE" "$rel" "$arch" "$b"
			done
			foot
		} >> "$reldir$arch/index.md"
	done
done

foot >> "$SITE/index.md"

echo "pages written:"
find "$SITE" -name index.md | sort | sed "s|^$SITE|  .|"
