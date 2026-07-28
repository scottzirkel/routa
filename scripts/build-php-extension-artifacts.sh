#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
repo="$PWD"

if (($# == 0)); then
	versions=(8.2.30 8.3.30 8.4.20 8.5.5)
else
	versions=("$@")
fi

case "$(uname -m)" in
	x86_64)
		asset_arch="amd64"
		;;
	aarch64 | arm64)
		asset_arch="arm64"
		;;
	*)
		echo "unsupported architecture: $(uname -m)" >&2
		exit 1
		;;
esac

dist="dist"
work="${RUNNER_TEMP:-/tmp}/routa-php-ext-${asset_arch}"
# static-php-cli can only build these as shared objects, so each one costs a
# tarball but they all come out of a single PHP compile per version.
extensions="${ROUTA_PHP_EXT_SHARED:-xdebug,pcov}"
base_extensions="${ROUTA_PHP_EXT_BASE_EXTENSIONS:-bcmath,zlib}"
spc_repo="${ROUTA_PHP_EXT_SPC_REPO:-https://github.com/crazywhalecc/static-php-cli.git}"
# Pin the toolchain: static-php-cli's main branch reorganizes without warning
# (bin/spc-gnu-docker vanished from main in July 2026), and an artifact build
# that silently changes toolchain between runs is not reproducible.
spc_ref="${ROUTA_PHP_EXT_SPC_REF:-2.8.5}"

IFS=',' read -r -a shared_exts <<< "$extensions"

mkdir -p "$dist"
rm -rf "$work"
mkdir -p "$work"

# One PHP compile serves every shared extension, so build them together and only
# fall back to one pass each when the combined build fails — an extension with no
# release for a new PHP (pcov trails PHP majors) must not cost us the others.
build_shared() {
	local list="$1"
	bin/spc-gnu-docker download --with-php="$version" --for-extensions="${base_extensions},${list}" --prefer-pre-built --retry=2 &&
		bin/spc-gnu-docker build "$base_extensions" --build-cli --build-shared="$list" --debug
}

package_ext() {
	local ext="$1"
	if [[ ! -f "buildroot/modules/${ext}.so" ]]; then
		return 1
	fi
	tar -C buildroot/modules -czf "$repo/$dist/routa_php_${ext}_${version}_linux_${asset_arch}.tar.gz" "${ext}.so"
}

for version in "${versions[@]}"; do
	build_dir="$work/php-$version"
	rm -rf "$build_dir"
	git clone --depth=1 --branch "$spc_ref" "$spc_repo" "$build_dir"
	chmod +x "$build_dir/bin/spc-gnu-docker"

	echo "building ${extensions} for PHP $version linux/$asset_arch"
	(
		cd "$build_dir"
		if build_shared "$extensions"; then
			for ext in "${shared_exts[@]}"; do
				package_ext "$ext" || echo "  warning: no ${ext}.so after building PHP $version" >&2
			done
		else
			echo "  warning: combined shared build failed for PHP $version; retrying one extension at a time" >&2
			for ext in "${shared_exts[@]}"; do
				# Package inside the loop: a later pass may clear buildroot/modules.
				if build_shared "$ext" && package_ext "$ext"; then
					continue
				fi
				echo "  warning: ${ext} did not build for PHP $version; skipping that artifact" >&2
			done
		fi
	)
done

shopt -s nullglob
built=("$dist"/routa_php_*_linux_"$asset_arch".tar.gz)
shopt -u nullglob
if ((${#built[@]} == 0)); then
	echo "no extension artifacts were produced for linux/$asset_arch" >&2
	exit 1
fi

(
	cd "$dist"
	sha256sum routa_php_*_linux_"$asset_arch".tar.gz > "routa_php_ext_linux_${asset_arch}_checksums.txt"
)

printf 'wrote %d artifact(s) for linux/%s:\n' "${#built[@]}" "$asset_arch"
printf '  %s\n' "${built[@]}"
