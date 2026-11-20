#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

version="${1:-$(git describe --tags --match 'v[0-9]*' --exact-match 2>/dev/null || git describe --tags --match 'v[0-9]*' --always)}"
version="${version#v}"
commit="$(git rev-parse --short HEAD)"
build_date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
pkg="github.com/scottzirkel/routa/cmd"
dist="dist"

mkdir -p "$dist"
rm -f "$dist"/routa_"$version"_linux_*.tar.gz "$dist"/routa_"$version"_checksums.txt

for arch in amd64 arm64; do
	bin_dir="$dist/routa_${version}_linux_${arch}"
	rm -rf "$bin_dir"
	mkdir -p "$bin_dir"

	echo "building routa ${version} for linux/${arch}"
	CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build \
		-trimpath \
		-ldflags "-s -w -X $pkg.Version=v$version -X $pkg.Commit=$commit -X $pkg.BuildDate=$build_date" \
		-o "$bin_dir/routa" .

	cp README.md RELEASE.md "$bin_dir/"
	tar -C "$bin_dir" -czf "$dist/routa_${version}_linux_${arch}.tar.gz" .
	rm -rf "$bin_dir"
done

(
	cd "$dist"
	sha256sum routa_"$version"_linux_*.tar.gz > "routa_${version}_checksums.txt"
)

echo "wrote $dist/routa_${version}_linux_*.tar.gz"
echo "wrote $dist/routa_${version}_checksums.txt"
