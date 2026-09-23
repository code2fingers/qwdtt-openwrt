#!/bin/sh

set -eu

FEEDNAME=qwdtt
# Both, on both SDKs. qwdtt-client would come in as a dependency of
# luci-proto-qwdtt anyway, but naming it explicitly is what makes it asserted
# below, and keeps it building if that dependency ever goes away.
PACKAGES='luci-proto-qwdtt qwdtt-client'
# The '#v11' is a git ref for docker build, and it pins the same tag the
# workflow does. Without it this tracked the action's default branch while CI
# tracked whatever it had at the time, so the two could diverge silently.
SDK_REPO=https://github.com/openwrt/gh-action-sdk.git#v11
DISTDIR=dist

# The output FORMAT follows the SDK version rather than any flag or option:
# OpenWrt replaced opkg with apk, so a 24.10 SDK emits .ipk and a 25.12 SDK
# emits .apk from identical sources. One architecture serves every target
# because the package sets LUCI_PKGARCH:=all.
#
# Both are pinned to a release, read from the same file the workflows read so
# a local build cannot target a different OpenWrt than CI does. The bare
# x86_64 tag would be the snapshot SDK, which does not ship the SDK inside the
# image and so downloads and extracts it on every run -- around six times the
# wall clock, for a moving target that makes a red build ambiguous.
TARGET_MAP=$(dirname "$0")/.github/openwrt-targets.json
command -v jq >/dev/null 2>&1 || { echo "jq is required to read $TARGET_MAP" >&2; exit 1; }
ARCH_IPK=x86_64-$(jq -r '."24.10".version' "$TARGET_MAP")
ARCH_APK=x86_64-$(jq -r '."25.12".version' "$TARGET_MAP")

# Git Bash rewrites arguments that look like absolute Unix paths into Windows
# paths, which would turn the container-side /feed into something like
# C:/Program Files/Git/feed. Ignored on Linux and macOS.
MSYS_NO_PATHCONV=1
export MSYS_NO_PATHCONV

# Run from the repository root whatever the caller's directory, since the whole
# root is the feed that gets mounted.
cd "$(dirname "$0")"

usage() {
	cat <<EOF
usage: ./build.sh <command> [package...]

  ipk     build .ipk  (SDK $ARCH_IPK): $PACKAGES
  apk     build .apk  (SDK $ARCH_APK): $PACKAGES
  all     build both
  shell   interactive shell in the SDK container (optional: SDK tag)
  clean   remove $DISTDIR/

Naming packages builds only those:

  ./build.sh apk qwdtt-client       just the client and the protocol handler
  ./build.sh apk luci-proto-qwdtt   just the web interface (and its translations)

Expect a modest saving, not a fast loop. Most of a local run is fixed SDK
setup -- feeds, defconfig and the toolchain -- which happens whatever is
being built. This is for checking that a package still assembles, not for
iterating. Iterate with 'go test' and 'go build' in client/, which take
seconds, and 'sh -n' on the shell scripts.

Output lands in $DISTDIR/<sdk>/bin/packages/*/$FEEDNAME/, one directory per
SDK so 'all' keeps both formats instead of the second wiping the first.
Requires Docker. The first run downloads the SDK image (~1 GB) and takes a
while; the packages themselves assemble in seconds.

The client binary is cross-compiled from client/ in this checkout before the
SDK runs, so a local edit is picked up with no commit, tag or hash to update
first. That needs a Go toolchain on the host.
EOF
}

# `pwd -W` prints a Windows path (C:/...) under MSYS, which is what Docker
# Desktop needs for a bind mount. Elsewhere it does not exist and plain pwd is
# already right.
host_dir() {
	pwd -W 2>/dev/null || pwd
}

build() {
	_arch=$1
	shift
	_image="sdk-$FEEDNAME:$_arch"
	_pkgs=$PACKAGES
	# An explicit list replaces the default rather than adding to it.
	if [ $# -gt 0 ]; then
		_pkgs="$*"
	fi

	# qwdtt-client installs a prebuilt binary, and luci-proto-qwdtt depends on
	# it, so the SDK needs one staged whatever is being built. Both SDKs here
	# are x86_64.
	echo "--- cross-compiling the client"
	( cd client && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -tags=openwrt -trimpath -ldflags="-s -w" \
		-o ../qwdtt-client/files/qwdtt-client . )
	# One output directory per SDK. `all` calls this twice and the entrypoint
	# ends with `mv bin/ /artifacts/`, so a single shared directory can only go
	# wrong both ways: cleaned before each build the second run deletes the
	# first's packages, and left in place the mv nests bin/ inside bin/.
	_out="$DISTDIR/$_arch"

	docker build --quiet --tag "$_image" --build-arg "ARCH=$_arch" "$SDK_REPO"

	# :? rather than plain expansion -- an empty value would make this rm -rf /.
	rm -rf "${_out:?}"
	mkdir -p "$_out"

	_host=$(host_dir)

	# NO_SHFMT_CHECK is not about shfmt: the block it guards also runs
	# `git -C <pkg>/files diff`, which refuses a tree owned by another uid
	# ("dubious ownership in /feed") and fails the checkout after it.
	docker run --rm \
		--env NO_SHFMT_CHECK=1 \
		--env "FEEDNAME=$FEEDNAME" \
		--env "PACKAGES=$_pkgs" \
		--volume "$_host:/feed" \
		--volume "$_host/$_out:/artifacts" \
		"$_image"

	echo "--- built:"
	find "$_out/bin" -type f \( -name '*.ipk' -o -name '*.apk' \) | sort

	# luci.mk emits the translation package from the LuCI one's po/ rather
	# than from a directory of its own, so it cannot be named in PACKAGES and
	# has to be expected here instead.
	_expect=$_pkgs
	case " $_pkgs " in
	*" luci-proto-$FEEDNAME "*) _expect="$_pkgs luci-i18n-$FEEDNAME-ru" ;;
	esac

	for _want in $_expect; do
		_n=$(find "$_out/bin" -type f \
			\( -name "$_want*.ipk" -o -name "$_want*.apk" \) | wc -l)
		if [ "$_n" -eq 0 ]; then
			echo "$_want was not produced" >&2
			exit 1
		fi
	done
}

_cmd=${1:-}
[ $# -gt 0 ] && shift

case "$_cmd" in
ipk)
	build "$ARCH_IPK" "$@" ;;
apk)
	build "$ARCH_APK" "$@" ;;
all)
	build "$ARCH_IPK" "$@"
	build "$ARCH_APK" "$@" ;;
clean)
	rm -rf "$DISTDIR" ;;
shell)
	# For diagnosing a failure from inside: the SDK and the mounted feed are
	# both present, and the entrypoint is bypassed.
	docker run --rm -it \
		--volume "$(host_dir):/feed" \
		--entrypoint /bin/bash \
		"ghcr.io/openwrt/sdk:${1:-$ARCH_IPK}" ;;
'' | -h | --help | help)
	usage ;;
*)
	echo "unknown command: $_cmd" >&2
	usage >&2
	exit 2 ;;
esac
