#!/usr/bin/env bash
# install.sh — Download, build, and install camctl from source.
#
# Usage:
#   bash <(curl -fsSL https://raw.githubusercontent.com/rohanthewiz/camctl/master/scripts/install.sh)
#
# Prerequisites: git, go (1.25.5+)
# Installs to: ~/bin/camctl (~/bin must be in PATH)

set -euo pipefail

REPO_URL="https://github.com/rohanthewiz/camctl.git"
INSTALL_DIR="$HOME/bin"
BINARY_NAME="camctl"

# --- Helper functions ---

info()  { printf "\033[1;34m==>\033[0m %s\n" "$1"; }
ok()    { printf "\033[1;32m==>\033[0m %s\n" "$1"; }
fail()  { printf "\033[1;31mError:\033[0m %s\n" "$1" >&2; exit 1; }

# --- Preflight checks ---

command -v git >/dev/null 2>&1 || fail "git is required but not found. Please install git first."
command -v go  >/dev/null 2>&1 || fail "go is required but not found. Install it from https://go.dev/dl/"

# Verify Go version is at least 1.25
go_version=$(go version | grep -oE 'go[0-9]+\.[0-9]+' | head -1 | sed 's/go//')
go_major=$(echo "$go_version" | cut -d. -f1)
go_minor=$(echo "$go_version" | cut -d. -f2)
if [ "$go_major" -lt 1 ] || { [ "$go_major" -eq 1 ] && [ "$go_minor" -lt 25 ]; }; then
    fail "Go 1.25+ is required (found $go_version). Update at https://go.dev/dl/"
fi

# --- Clone and build ---

TMPDIR=$(mktemp -d)
# Clean up the temp directory on exit regardless of success or failure
trap 'rm -rf "$TMPDIR"' EXIT

info "Cloning camctl repository..."
git clone --depth 1 "$REPO_URL" "$TMPDIR/camctl"

info "Building camctl..."
cd "$TMPDIR/camctl"
go build -o "$TMPDIR/$BINARY_NAME" .

# --- Install ---

mkdir -p "$INSTALL_DIR"
mv "$TMPDIR/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
chmod +x "$INSTALL_DIR/$BINARY_NAME"

ok "camctl installed to $INSTALL_DIR/$BINARY_NAME"

# Verify it's reachable via PATH
if command -v camctl >/dev/null 2>&1; then
    ok "Ready! Run 'camctl' to launch."
else
    printf "\n\033[1;33mNote:\033[0m %s is not in your PATH.\n" "$INSTALL_DIR"
    echo "Add it by appending this line to your shell profile (~/.bashrc, ~/.zshrc, etc.):"
    echo ""
    echo "  export PATH=\"\$HOME/bin:\$PATH\""
    echo ""
    echo "Then restart your terminal and run 'camctl'."
fi