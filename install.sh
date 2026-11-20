#!/usr/bin/env bash
# Build routa and symlink it into ~/.local/bin so you can run it from anywhere.
# Re-running picks up the latest build automatically (the symlink doesn't change).
set -e

cd "$(dirname "$0")"

VERSION="$(git describe --tags --match 'v[0-9]*' --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
PKG="github.com/scottzirkel/routa/cmd"

echo "→ building routa ($VERSION, $COMMIT)"
go build -ldflags "-X $PKG.Version=$VERSION -X $PKG.Commit=$COMMIT -X $PKG.BuildDate=$BUILD_DATE" -o routa .

mkdir -p "$HOME/.local/bin"
target="$(realpath ./routa)"
ln -sf "$target" "$HOME/.local/bin/routa"
echo "✓ ~/.local/bin/routa → $target"

case ":$PATH:" in
    *":$HOME/.local/bin:"*)
        echo "✓ ~/.local/bin is on \$PATH"
        ;;
    *)
        echo
        echo "⚠  ~/.local/bin is NOT on \$PATH. Add this to your shell rc:"
        echo "     export PATH=\"\$HOME/.local/bin:\$PATH\""
        ;;
esac

echo
echo "Test: routa version && routa status"
