#!/bin/sh
# Install the echos CLI. Usage:
#   curl -fsSL https://raw.githubusercontent.com/heurema/echos/main/install.sh | sh
#
# Overrides:
#   ECHOS_INSTALL_DIR=/usr/local/bin   where to install (default: ~/.local/bin)
set -eu

REPO="heurema/echos"
BASE="https://github.com/${REPO}/releases/latest/download"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) echo "echos: unsupported architecture: $arch" >&2; exit 1 ;;
esac
case "$os" in
	darwin | linux) ;;
	*) echo "echos: unsupported OS: $os (build from source: go install github.com/heurema/echos/cmd/echos@latest)" >&2; exit 1 ;;
esac

asset="echos-${os}-${arch}"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "Downloading ${asset}..."
if ! curl -fsSL "${BASE}/${asset}" -o "$tmp/echos"; then
	echo "echos: download failed for ${asset}" >&2
	exit 1
fi

# Verify the checksum when the release publishes one.
if curl -fsSL "${BASE}/checksums.txt" -o "$tmp/checksums.txt" 2>/dev/null; then
	want=$(grep " ${asset}\$" "$tmp/checksums.txt" 2>/dev/null | awk '{print $1}' || true)
	if [ -n "${want:-}" ]; then
		if command -v sha256sum >/dev/null 2>&1; then
			got=$(sha256sum "$tmp/echos" | awk '{print $1}')
		else
			got=$(shasum -a 256 "$tmp/echos" | awk '{print $1}')
		fi
		if [ "$want" != "$got" ]; then
			echo "echos: checksum mismatch (expected $want, got $got)" >&2
			exit 1
		fi
		echo "Checksum verified."
	fi
fi

chmod +x "$tmp/echos"

dir="${ECHOS_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$dir" 2>/dev/null || true
if [ -w "$dir" ]; then
	mv "$tmp/echos" "$dir/echos"
else
	echo "Installing to $dir (requires sudo)..."
	sudo mv "$tmp/echos" "$dir/echos"
fi

echo "Installed echos to $dir/echos"
case ":$PATH:" in
	*":$dir:"*) ;;
	*) echo "Note: $dir is not on your PATH — add it, e.g. export PATH=\"$dir:\$PATH\"" ;;
esac

echo
echo "Get started:"
echo "  echos id            # create your identity (uses https://echos.heurema.dev)"
echo "  echos sessions      # list your Claude Code / Codex sessions"
