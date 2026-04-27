#!/usr/bin/env bash
# Build hostr and symlink it into ~/.local/bin so you can run it from anywhere.
# Re-running picks up the latest build automatically (the symlink doesn't change).
set -e

cd "$(dirname "$0")"

echo "→ building hostr"
go build -o hostr .

mkdir -p "$HOME/.local/bin"
target="$(realpath ./hostr)"
ln -sf "$target" "$HOME/.local/bin/hostr"
echo "✓ ~/.local/bin/hostr → $target"

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
echo "Test: hostr status"
