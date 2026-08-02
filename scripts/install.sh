#!/usr/bin/env bash
# install.sh — Download, build, and install camctl from source.
#
# Usage:
#   bash <(curl -fsSL https://raw.githubusercontent.com/rohanthewiz/camctl/master/scripts/install.sh)
#
# Prerequisites: git, go (1.26.1+)
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

# Verify Go version is at least 1.26 (the bytdb storage engine's floor)
go_version=$(go version | grep -oE 'go[0-9]+\.[0-9]+' | head -1 | sed 's/go//')
go_major=$(echo "$go_version" | cut -d. -f1)
go_minor=$(echo "$go_version" | cut -d. -f2)
if [ "$go_major" -lt 1 ] || { [ "$go_major" -eq 1 ] && [ "$go_minor" -lt 26 ]; }; then
    fail "Go 1.26+ is required (found $go_version). Update at https://go.dev/dl/"
fi

# --- Clone and build ---

TMPDIR=$(mktemp -d)
# Clean up the temp directory on exit regardless of success or failure
trap 'rm -rf "$TMPDIR"' EXIT

info "Cloning camctl repository..."
git clone --depth 1 "$REPO_URL" "$TMPDIR/camctl-src"

info "Building camctl..."
cd "$TMPDIR/camctl-src"
# The "ndi" tag compiles the preview subsystem (NDI/OBS/HTTP/RTSP strategies);
# without it the binary is control-only with no in-app preview. NDI itself is
# loaded dynamically at runtime via purego, so the tag adds no build deps.
# Matches the flags used by mac-install.sh.
#
# CGO_ENABLED=0: storage is pure-Go bytdb, so the binary needs no C toolchain.
# Anything that reintroduces a CGo dependency should fail loudly here.
CGO_ENABLED=0 go build -trimpath -tags ndi -ldflags "-s -w" -o "$TMPDIR/$BINARY_NAME" .

# --- Install ---

mkdir -p "$INSTALL_DIR"
mv "$TMPDIR/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
chmod +x "$INSTALL_DIR/$BINARY_NAME"

ok "camctl installed to $INSTALL_DIR/$BINARY_NAME"

# --- One-time conversion of a DuckDB-era database ---
#
# camctl moved its storage from DuckDB to bytdb; the file formats are unrelated,
# so an existing ~/.camctl/camctl.db has to be converted rather than opened.
# Runs only when the old file exists and the new one does not, so re-running the
# installer never touches live data. The old file is read, never modified.
#
# Non-fatal by design: the app is already installed and working at this point,
# and a conversion failure should not undo that. Unlike the app itself, the
# DuckDB reader needs CGo, which is why this is the one step that may want a
# C toolchain — and only for users who have old data.
DATA_DIR="$HOME/.camctl"
if [ -f "$DATA_DIR/camctl.db" ] && [ ! -f "$DATA_DIR/camctl.bytdb" ]; then
    info "converting legacy DuckDB database to bytdb..."
    if ( cd "$TMPDIR/camctl-src" && CGO_ENABLED=1 go run ./cmd/dbmigrate \
           -from "$DATA_DIR/camctl.db" -to "$DATA_DIR/camctl.bytdb" ); then
        ok "converted $DATA_DIR/camctl.db (original kept)"
    else
        printf "\n\033[1;33mNote:\033[0m could not convert %s\n" "$DATA_DIR/camctl.db"
        echo "camctl will start with an empty camera list; your old data is untouched."
        echo "Retry from a clone with: go run ./cmd/dbmigrate"
    fi
fi

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