#!/usr/bin/env bash
# Build AccurateReviewer and install/uninstall it system-wide as `ar`.
#
# Usage:
#   ./setup.sh                  # build + install (auto-pick dir)
#   ./setup.sh --prefix DIR     # install into DIR/bin
#   ./setup.sh --uninstall      # remove installed `ar`
#
# Install location preference:
#   1. --prefix DIR              (explicit)
#   2. $AR_INSTALL_DIR           (env override)
#   3. /usr/local/bin            (if writable, or sudo available)
#   4. $HOME/.local/bin          (fallback; ensure it's on PATH)

set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC_DIR="$REPO_DIR/src"
BIN_DIR="$REPO_DIR/bin"
ARTIFACT="$BIN_DIR/accurate-reviewer"
COMMAND_NAME="ar"

PREFIX=""
UNINSTALL=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --prefix)
            PREFIX="${2:-}"
            shift 2
            ;;
        --prefix=*)
            PREFIX="${1#--prefix=}"
            shift
            ;;
        --uninstall)
            UNINSTALL=1
            shift
            ;;
        -h|--help)
            sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        *)
            echo "unknown argument: $1" >&2
            exit 2
            ;;
    esac
done

find_existing_install() {
    # Prefer the copy on PATH if it points at a real file.
    local on_path
    on_path="$(command -v "$COMMAND_NAME" 2>/dev/null || true)"
    if [[ -n "$on_path" && -f "$on_path" ]]; then
        dirname "$on_path"
        return 0
    fi
    # Fall back to known install dirs.
    local dir
    for dir in "${AR_INSTALL_DIR:-}" /usr/local/bin "$HOME/.local/bin"; do
        [[ -n "$dir" ]] || continue
        if [[ -e "$dir/$COMMAND_NAME" ]]; then
            echo "$dir"
            return 0
        fi
    done
    return 1
}

pick_install_dir() {
    if [[ -n "$PREFIX" ]]; then
        echo "$PREFIX/bin"
        return
    fi
    # Update in place if `ar` is already installed and no --prefix was given.
    local existing
    if existing="$(find_existing_install)"; then
        echo "$existing"
        return
    fi
    if [[ -n "${AR_INSTALL_DIR:-}" ]]; then
        echo "$AR_INSTALL_DIR"
        return
    fi
    if [[ -w /usr/local/bin ]] 2>/dev/null; then
        echo "/usr/local/bin"
        return
    fi
    if command -v sudo >/dev/null 2>&1 && [[ -d /usr/local/bin ]]; then
        echo "/usr/local/bin"
        return
    fi
    echo "$HOME/.local/bin"
}

install_into() {
    local dest_dir="$1"
    local dest="$dest_dir/$COMMAND_NAME"
    mkdir -p "$dest_dir" 2>/dev/null || true

    if [[ -w "$dest_dir" ]]; then
        install -m 0755 "$ARTIFACT" "$dest"
    else
        echo "Elevating with sudo to write $dest_dir ..."
        sudo install -m 0755 "$ARTIFACT" "$dest"
    fi
    echo "$dest"
}

remove_from() {
    local dir="$1"
    local target="$dir/$COMMAND_NAME"
    [[ -e "$target" || -L "$target" ]] || return 1
    if [[ -w "$dir" ]]; then
        rm -f "$target"
    else
        echo "Elevating with sudo to remove $target ..."
        sudo rm -f "$target"
    fi
    echo "Removed $target"
    return 0
}

if [[ $UNINSTALL -eq 1 ]]; then
    candidate_dirs=()
    if [[ -n "$PREFIX" ]]; then
        candidate_dirs+=("$PREFIX/bin")
    else
        [[ -n "${AR_INSTALL_DIR:-}" ]] && candidate_dirs+=("$AR_INSTALL_DIR")
        candidate_dirs+=("/usr/local/bin" "$HOME/.local/bin")
    fi

    removed=0
    for dir in "${candidate_dirs[@]}"; do
        if remove_from "$dir" 2>/dev/null; then
            removed=1
        fi
    done

    if [[ $removed -eq 0 ]]; then
        echo "No '$COMMAND_NAME' binary found in: ${candidate_dirs[*]}"
        exit 1
    fi
    exit 0
fi

if ! command -v go >/dev/null 2>&1; then
    echo "error: 'go' is not on PATH; install Go before running this script." >&2
    exit 1
fi

VERSION_VAL="$(tr -d '[:space:]' < "$REPO_DIR/VERSION" 2>/dev/null || echo dev)"
COMMIT_VAL="$(git -C "$REPO_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)"
LDFLAGS="-X 'github.com/scaratec/accurate-reviewer/internal/cli.Version=${VERSION_VAL}' -X 'github.com/scaratec/accurate-reviewer/internal/cli.Commit=${COMMIT_VAL}'"

echo "Building $ARTIFACT (version $VERSION_VAL, commit $COMMIT_VAL) ..."
mkdir -p "$BIN_DIR"
(cd "$SRC_DIR" && go mod tidy >/dev/null && go build -ldflags "$LDFLAGS" -o "$ARTIFACT" ./cmd/accurate-reviewer/)
echo "Build complete: $ARTIFACT"

DEST_DIR="$(pick_install_dir)"
ACTION="Installed"
[[ -e "$DEST_DIR/$COMMAND_NAME" ]] && ACTION="Updated"
INSTALLED_PATH="$(install_into "$DEST_DIR")"
echo "$ACTION: $INSTALLED_PATH"

if ! command -v "$COMMAND_NAME" >/dev/null 2>&1; then
    echo
    echo "warning: '$COMMAND_NAME' is not on your PATH."
    echo "Add this line to your shell rc (e.g. ~/.bashrc or ~/.zshrc):"
    echo "    export PATH=\"$DEST_DIR:\$PATH\""
fi

echo
echo "Done. Try:  $COMMAND_NAME --help"
