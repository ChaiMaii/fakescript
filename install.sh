#!/usr/bin/env sh
# FakeScript + fkspm Installer for Linux / macOS
# Usage: curl -fsSL https://raw.githubusercontent.com/ChaiMaii/fakescript/main/install.sh | sh

set -eu

if ! command -v go >/dev/null 2>&1; then
  printf '\033[1;31mERROR:\033[0m Go 1.22+ is required to install FakeScript and fkspm.\n' >&2
  printf 'Install Go from https://go.dev/dl/ and re-run this script.\n' >&2
  exit 1
fi

TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t fakescript_setup)"
cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT INT TERM

INSTALLER_URL="https://raw.githubusercontent.com/ChaiMaii/fakescript/main/installer/main.go"
INSTALLER_GO="$TMP_DIR/main.go"
INSTALLER_BIN="$TMP_DIR/fakescript-installer"

printf 'Fetching FakeScript visual installer...\n'
if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$INSTALLER_URL" -o "$INSTALLER_GO"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "$INSTALLER_GO" "$INSTALLER_URL"
else
  printf '\033[1;31mERROR:\033[0m curl or wget is required.\n' >&2
  exit 1
fi

go build -o "$INSTALLER_BIN" "$INSTALLER_GO"
"$INSTALLER_BIN" "$@"
