#!/bin/sh
# Install ciguse on macOS or Linux:
#   curl -fsSL https://raw.githubusercontent.com/demetriusk/ciguse/main/install.sh | sh
# Options (environment): CIGUSE_VERSION=v0.1.0 to pin a release, CIGUSE_INSTALL_DIR to choose where it goes.
set -eu

repo="demetriusk/ciguse"
dir="${CIGUSE_INSTALL_DIR:-$HOME/.local/bin}"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "ciguse: unsupported OS $(uname -s). On Windows use install.ps1." >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "ciguse: unsupported CPU $(uname -m)" >&2; exit 1 ;;
esac

if [ -n "${CIGUSE_VERSION:-}" ]; then
  base="https://github.com/$repo/releases/download/$CIGUSE_VERSION"
else
  base="https://github.com/$repo/releases/latest/download"
fi
archive="ciguse_${os}_${arch}.tar.gz"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$base/$archive" -o "$tmp/$archive"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

want="$(grep " $archive\$" "$tmp/checksums.txt" | cut -d ' ' -f 1)"
if command -v sha256sum >/dev/null 2>&1; then
  got="$(sha256sum "$tmp/$archive" | cut -d ' ' -f 1)"
else
  got="$(shasum -a 256 "$tmp/$archive" | cut -d ' ' -f 1)"
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "ciguse: checksum mismatch for $archive, not installing" >&2
  exit 1
fi

tar -xzf "$tmp/$archive" -C "$tmp" ciguse
mkdir -p "$dir"
mv "$tmp/ciguse" "$dir/ciguse"
chmod 755 "$dir/ciguse"

shown="$dir/ciguse"
case "$shown" in "$HOME"/*) shown="~${shown#"$HOME"}" ;; esac
echo "ciguse installed to $shown ($("$dir/ciguse" version))"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "Add it to your PATH:  export PATH=\"$dir:\$PATH\"" ;;
esac
