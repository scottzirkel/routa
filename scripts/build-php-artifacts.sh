#!/usr/bin/env bash
set -euo pipefail

# Build routa's own PHP for Linux: the CLI, PHP-FPM, and the shared extensions
# routa manages (Xdebug, pcov), all from one static-php-cli compile per version.
#
# static-php-cli's glibc toolchain links glibc dynamically, so both binaries can
# load shared extensions. That is the difference from the old dl.static-php.dev
# gnu-bulk builds, which linked glibc statically and could load nothing.
#
# Usage:
#   scripts/build-php-artifacts.sh 8.5            # latest 8.5 patch, per php.net
#   scripts/build-php-artifacts.sh 8.5.13 8.6.0   # exact patches
#
# Writes to dist/:
#   php-<ver>-cli-linux-<arch>.tar.gz     php
#   php-<ver>-fpm-linux-<arch>.tar.gz     php-fpm
#   php-<ver>-linux-<arch>.sha256         checksums for the two archives above
#   routa_php_<ext>_<ver>_linux_<goarch>.tar.gz   one per shared extension
#
# The PHP archive names match the old gnu-bulk channel, so routa reads both
# sources with the same parser.

cd "$(dirname "$0")/.."
repo="$PWD"

if (($# == 0)); then
	echo "usage: $(basename "$0") <php-version>...   # e.g. 8.5 or 8.5.13" >&2
	exit 1
fi

case "$(uname -m)" in
	x86_64)
		arch="x86_64"
		asset_arch="amd64"
		;;
	aarch64 | arm64)
		arch="aarch64"
		asset_arch="arm64"
		;;
	*)
		echo "unsupported architecture: $(uname -m)" >&2
		exit 1
		;;
esac

dist="${ROUTA_PHP_DIST:-$repo/dist}"
work="${RUNNER_TEMP:-/tmp}/routa-php-build-${asset_arch}"
extensions="${ROUTA_PHP_EXTENSIONS:-$(tr -d '[:space:]' < scripts/php-extensions.txt)}"
# Extensions static-php-cli can only build as shared objects. Each becomes its
# own archive, and routa downloads them beside the PHP install.
shared="${ROUTA_PHP_SHARED:-xdebug,pcov}"
spc_repo="${ROUTA_PHP_SPC_REPO:-https://github.com/crazywhalecc/static-php-cli.git}"
# Pin the toolchain: static-php-cli's main branch reorganizes without warning
# (bin/spc-gnu-docker vanished from main in July 2026), and a build that
# silently changes toolchain between runs is not reproducible.
spc_ref="${ROUTA_PHP_SPC_REF:-2.8.5}"
# static-php-cli takes Xdebug's newest GitHub release, which can be an alpha
# (3.6.0alpha1 in November 2026). Take PECL's stable release instead.
xdebug_version="${ROUTA_PHP_XDEBUG_VERSION:-}"

IFS=',' read -r -a shared_exts <<< "$shared"

if [[ ",$shared," == *",xdebug,"* && -z "$xdebug_version" ]]; then
	xdebug_version="$(curl -fsSL https://pecl.php.net/rest/r/xdebug/stable.txt)"
	if [[ ! "$xdebug_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
		echo "could not read Xdebug's stable version from PECL" >&2
		exit 1
	fi
fi

# resolve_version turns "8.5" into the newest 8.5 patch php.net has released.
resolve_version() {
	local spec="$1" resolved
	if [[ "$spec" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
		echo "$spec"
		return
	fi
	if [[ ! "$spec" =~ ^[0-9]+\.[0-9]+$ ]]; then
		echo "invalid PHP version: $spec (want 8.5 or 8.5.13)" >&2
		return 1
	fi
	resolved="$(curl -fsSL "https://www.php.net/releases/index.php?json&version=${spec}" | jq -r '.version // empty')"
	if [[ ! "$resolved" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
		echo "php.net lists no release for PHP $spec" >&2
		return 1
	fi
	echo "$resolved"
}

# spc_build compiles PHP with the given shared extensions (comma separated, may
# be empty). Run from inside a static-php-cli checkout.
spc_build() {
	local shared_list="$1" wanted="$extensions"
	local -a pins=()
	if [[ -n "$shared_list" ]]; then
		wanted="${extensions},${shared_list}"
	fi
	if [[ ",$shared_list," == *",xdebug,"* ]]; then
		pins+=(--custom-url="xdebug:https://pecl.php.net/get/xdebug-${xdebug_version}.tgz")
	fi
	bin/spc-gnu-docker download --with-php="$version" --for-extensions="$wanted" "${pins[@]}" --prefer-pre-built --retry=2 &&
		bin/spc-gnu-docker build "$extensions" --build-cli --build-fpm ${shared_list:+--build-shared="$shared_list"} --debug
}

# package_bins checks the two binaries before archiving them, so a build that
# came out wrong never reaches a release.
package_bins() {
	local bin got
	for bin in php php-fpm; do
		if [[ ! -x "buildroot/bin/$bin" ]]; then
			echo "build produced no buildroot/bin/$bin" >&2
			return 1
		fi
		# No ELF interpreter means libc is linked statically, and such a binary
		# refuses every shared extension. That is the exact failure these builds
		# exist to fix. (Process substitution, not a pipe: grep -q exiting early
		# would fail a pipe under pipefail.)
		if ! LC_ALL=C grep -qa "ld-linux" < <(head -c 4096 "buildroot/bin/$bin"); then
			echo "buildroot/bin/$bin is not dynamically linked; it cannot load shared extensions" >&2
			return 1
		fi
	done
	got="$(buildroot/bin/php -n -r 'echo PHP_VERSION;')"
	if [[ "$got" != "$version" ]]; then
		echo "built CLI reports PHP $got, expected $version" >&2
		return 1
	fi
	got="$(buildroot/bin/php-fpm -n -v)"
	if [[ "$got" != "PHP $version (fpm-fcgi)"* ]]; then
		echo "built php-fpm does not report PHP $version: ${got%%$'\n'*}" >&2
		return 1
	fi
	tar -C buildroot/bin -czf "$dist/php-${version}-cli-linux-${arch}.tar.gz" php
	tar -C buildroot/bin -czf "$dist/php-${version}-fpm-linux-${arch}.tar.gz" php-fpm
	(
		cd "$dist"
		sha256sum "php-${version}-cli-linux-${arch}.tar.gz" "php-${version}-fpm-linux-${arch}.tar.gz" \
			> "php-${version}-linux-${arch}.sha256"
	)
}

# package_ext archives one shared extension after checking the CLI loads it.
package_ext() {
	local ext="$1" so load_key="extension" modules got
	so="$PWD/buildroot/modules/${ext}.so"
	if [[ ! -f "$so" ]]; then
		echo "  warning: no ${ext}.so after building PHP $version" >&2
		return 1
	fi
	if [[ "$ext" == "xdebug" ]]; then
		load_key="zend_extension"
	fi
	if [[ "$ext" == "xdebug" ]]; then
		got="$(buildroot/bin/php -n -d "zend_extension=${so}" -r 'echo phpversion("xdebug");')"
		if [[ "$got" != "$xdebug_version" ]]; then
			echo "  warning: built Xdebug is $got, expected $xdebug_version" >&2
			return 1
		fi
	fi
	modules="$(buildroot/bin/php -n -d "${load_key}=${so}" -m)"
	if ! grep -qix "$ext" <<< "$modules"; then
		echo "  warning: the built CLI could not load ${ext}.so" >&2
		return 1
	fi
	tar -C buildroot/modules -czf "$dist/routa_php_${ext}_${version}_linux_${asset_arch}.tar.gz" "${ext}.so"
}

mkdir -p "$dist" "$work"

for spec in "$@"; do
	version="$(resolve_version "$spec")"
	build_dir="$work/php-$version"
	if ! rm -rf "$build_dir"; then
		echo "cannot remove $build_dir; an earlier Docker build left root-owned files there. Remove it with sudo." >&2
		exit 1
	fi
	git clone --quiet --depth=1 --branch "$spc_ref" "$spc_repo" "$build_dir"
	chmod +x "$build_dir/bin/spc-gnu-docker"

	echo "building PHP $version (shared: ${shared:-none}) for linux/$arch"
	(
		cd "$build_dir"
		if [[ -n "$shared" ]] && spc_build "$shared"; then
			package_bins
			for ext in "${shared_exts[@]}"; do
				package_ext "$ext" || true
			done
			exit 0
		fi

		# One extension with no release for a new PHP (pcov trails PHP majors)
		# must not cost us the others, or PHP itself. Build each alone and keep
		# the binaries from the first compile that works.
		[[ -n "$shared" ]] && echo "  warning: combined build failed for PHP $version; retrying one shared extension at a time" >&2
		have_bins=false
		for ext in "${shared_exts[@]}"; do
			if ! spc_build "$ext"; then
				echo "  warning: ${ext} did not build for PHP $version; skipping it" >&2
				continue
			fi
			if ! $have_bins; then
				package_bins
				have_bins=true
			fi
			package_ext "$ext" || true
		done
		if ! $have_bins; then
			spc_build ""
			package_bins
		fi
	)
done

shopt -s nullglob
echo "wrote to $dist:"
for f in "$dist"/php-*-linux-"$arch".* "$dist"/routa_php_*_linux_"$asset_arch".tar.gz; do
	echo "  ${f##*/}"
done
