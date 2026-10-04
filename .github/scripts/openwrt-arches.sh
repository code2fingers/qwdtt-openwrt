#!/bin/sh
# One JSON line per pkgarch the releases publish, with the Go environment that builds for it.
# Derived from the pkgarch name: OpenWrt publishes the CPU and FPU nowhere else.

set -eu

[ $# -gt 0 ] || { echo "usage: openwrt-arches.sh <release>..." >&2; exit 2; }

# Softfloat unless the name advertises an FPU: hardfloat on a softfloat target dies on the first FP instruction.
go_env_for() { # <pkgarch> -> "GOARCH GOARM GOMIPS GOMIPS64", "." for unset
	case "$1" in
	x86_64)            echo "amd64 . . ." ;;
	i386_*)            echo "386 . . ." ;;
	aarch64_*)         echo "arm64 . . ." ;;
	loongarch64_*)     echo "loong64 . . ." ;;
	riscv64_*)         echo "riscv64 . . ." ;;
	powerpc64_*)       echo "ppc64 . . ." ;;

	# GOARM 7 needs VFPv3 or better, 6 needs VFPv1, 5 is software floating
	# point. Only the names that advertise an FPU get more than 5.
	arm_*neon*|arm_*vfpv3*|arm_*vfpv4*)  echo "arm 7 . ." ;;
	arm_arm1176jzf-s_vfp)                echo "arm 6 . ." ;;
	arm_*)                               echo "arm 5 . ." ;;

	# 64-bit MIPS reads GOMIPS64, not GOMIPS. Emitting the wrong one is
	# silent: Go ignores it and falls back to hardfloat.
	mips64el_*)        echo "mips64le . . softfloat" ;;
	mips64_*)          echo "mips64 . . softfloat" ;;
	mipsel_*)          echo "mipsle . softfloat ." ;;
	mips_*)            echo "mips . softfloat ." ;;

	# Go has no linux/ppc (32-bit) and no big-endian ARM, so these cannot be
	# built at all rather than being an omission to fix later.
	powerpc_*|armeb_*) return 1 ;;
	*)                 return 1 ;;
	esac
}

for release in "$@"; do
	index=$(curl -fsS "https://downloads.openwrt.org/releases/$release/packages/") || {
		echo "cannot list architectures for $release" >&2
		exit 1
	}

	arches=$(printf '%s' "$index" |
		grep -oE 'href="[A-Za-z0-9_.-]+/"' |
		sed 's/href="//; s#/"##' |
		grep -vE '^\.\.?$' | sort -u)

	[ -n "$arches" ] || { echo "no architectures found for $release" >&2; exit 1; }

	for arch in $arches; do
		if ! spec=$(go_env_for "$arch"); then
			echo "skipping $arch on $release: no Go target" >&2
			continue
		fi
		goarch=${spec%% *}; spec=${spec#* }
		goarm=${spec%% *};  spec=${spec#* }
		gomips=${spec%% *}
		gomips64=${spec#* }

		printf '{"sdk":"%s-%s","arch":"%s","release":"%s","goarch":"%s"' \
			"$arch" "$release" "$arch" "$release" "$goarch"
		[ "$goarm" = "." ]    || printf ',"goarm":"%s"' "$goarm"
		[ "$gomips" = "." ]   || printf ',"gomips":"%s"' "$gomips"
		[ "$gomips64" = "." ] || printf ',"gomips64":"%s"' "$gomips64"
		printf '}\n'
	done
done
