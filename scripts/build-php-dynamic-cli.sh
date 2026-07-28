#!/usr/bin/env bash
set -euo pipefail

# Build a dynamically-linked PHP CLI that can load shared extensions.
#
# The upstream gnu-bulk binaries link glibc statically, and Linux cannot dlopen
# into such a binary, so they can never load pcov or Xdebug regardless of ini
# settings. routa prefers a bin/php-dynamic over the stock bin/php when present,
# leaving php-fpm on the stock static build that serves sites.

cd "$(dirname "$0")/.."
repo="$PWD"

if (($# != 1)); then
	echo "usage: $(basename "$0") <exact-php-version>   # e.g. 8.4.20" >&2
	exit 1
fi
version="$1"

case "$(uname -m)" in
	x86_64) asset_arch="amd64" ;;
	aarch64 | arm64) asset_arch="arm64" ;;
	*)
		echo "unsupported architecture: $(uname -m)" >&2
		exit 1
		;;
esac

dist="dist"
work="${RUNNER_TEMP:-/tmp}/routa-php-dyn-${asset_arch}"
shared="${ROUTA_PHP_DYN_SHARED:-pcov}"
spc_repo="${ROUTA_PHP_EXT_SPC_REPO:-https://github.com/crazywhalecc/static-php-cli.git}"
spc_ref="${ROUTA_PHP_EXT_SPC_REF:-2.8.5}"

# Track the channel's own extension list so the dynamic CLI and the stock FPM
# binary agree. A CLI missing an extension FPM has fails only under test, which
# is the worst place to discover it.
bulk_readme="https://dl.static-php.dev/static-php-cli/gnu-bulk/README.txt"
fallback_exts="apcu,bcmath,bz2,calendar,ctype,curl,dba,dom,event,exif,fileinfo,filter,ftp,gd,gmp,iconv,imagick,imap,intl,mbregex,mbstring,mysqli,mysqlnd,opcache,openssl,opentelemetry,pcntl,pdo,pdo_mysql,pgsql,phar,posix,protobuf,readline,redis,session,shmop,simplexml,soap,sockets,sodium,sqlite3,swoole,swoole-hook-mysql,swoole-hook-pgsql,swoole-hook-sqlite,sysvmsg,sysvsem,sysvshm,tokenizer,xml,xmlreader,xmlwriter,xsl,zip,zlib"

extensions="${ROUTA_PHP_DYN_EXTENSIONS:-}"
if [[ -z "$extensions" ]]; then
	# The list sits on its own line beneath a "uses extensions:" heading.
	extensions="$(curl -fsSL "$bulk_readme" 2>/dev/null |
		grep -oE '^[a-z0-9][a-z0-9,_-]{40,}$' | head -1 || true)"
fi
if [[ -z "$extensions" ]]; then
	echo "could not read the gnu-bulk extension list; using the pinned fallback" >&2
	extensions="$fallback_exts"
fi

build_dir="$work/php-$version"
rm -rf "$build_dir"
mkdir -p "$work" "$dist"
git clone --depth=1 --branch "$spc_ref" "$spc_repo" "$build_dir"
chmod +x "$build_dir/bin/spc-gnu-docker"

echo "building dynamic PHP $version CLI (shared: $shared) for linux/$asset_arch"
(
	cd "$build_dir"
	bin/spc-gnu-docker download --with-php="$version" --for-extensions="${extensions},${shared}" --prefer-pre-built --retry=2
	bin/spc-gnu-docker build "$extensions" --build-cli --build-shared="$shared" --debug

	# A CLI with no ELF interpreter links libc statically and would load nothing,
	# which is the exact failure this script exists to avoid. Catch it here rather
	# than after it has been installed.
	if ! head -c 4096 buildroot/bin/php | grep -qa "ld-linux"; then
		echo "built CLI is not dynamically linked; it cannot load shared extensions" >&2
		exit 1
	fi

	tar -C buildroot/bin -czf "$repo/$dist/routa_php_dynamic_cli_${version}_linux_${asset_arch}.tar.gz" php
	for ext in ${shared//,/ }; do
		test -f "buildroot/modules/${ext}.so"
		tar -C buildroot/modules -czf "$repo/$dist/routa_php_${ext}_${version}_linux_${asset_arch}.tar.gz" "${ext}.so"
	done
)

cat <<EOF

wrote $dist/routa_php_dynamic_cli_${version}_linux_${asset_arch}.tar.gz

Install it for an existing routa PHP build with:
  tar -xzOf $dist/routa_php_dynamic_cli_${version}_linux_${asset_arch}.tar.gz php \\
    > ~/.local/share/routa/php/${version}/bin/php-dynamic
  chmod +x ~/.local/share/routa/php/${version}/bin/php-dynamic
EOF
