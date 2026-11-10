#!/usr/bin/env sh
# ney installer for macOS and Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/naay99999/neything/main/scripts/install.sh | sh
#
# Installs to ~/.local/bin (no sudo), adds it to PATH in your shell rc if
# needed, verifies the release SHA-256, then starts `ney init` on a first
# install. Re-running it updates ney in place.
#
# Environment:
#   NEY_VERSION=vX.Y.Z     install that version instead of the latest
#   NEY_INSTALL_DIR=DIR    install somewhere else (sudo is used only if DIR is not writable)
#   NEY_NO_MODIFY_PATH=1   never touch shell rc files
#   NEY_NO_INIT=1          do not start `ney init`
set -e

REPO="naay99999/neything"
INSTALL_DIR="${NEY_INSTALL_DIR:-$HOME/.local/bin}"
INSTALL_DIR="${INSTALL_DIR%/}"
TARGET="$INSTALL_DIR/ney"

say()  { printf '%s\n' "$*"; }
warn() { printf '%s\n' "$*" >&2; }
die()  { warn "$*"; exit 1; }

command -v curl >/dev/null 2>&1 || die "curl is required."
command -v tar  >/dev/null 2>&1 || die "tar is required."

# --- platform -------------------------------------------------------------
OS="$(uname -s)"
case "$OS" in
  Linux)  os="linux"  ;;
  Darwin) os="darwin" ;;
  MINGW*|MSYS*|CYGWIN*) die "On Windows, install from PowerShell instead:
  irm https://raw.githubusercontent.com/naay99999/neything/main/scripts/install.ps1 | iex" ;;
  *) die "Unsupported OS: $OS (this installer covers macOS and Linux)." ;;
esac

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)  arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) die "Unsupported architecture: $ARCH" ;;
esac

# --- version --------------------------------------------------------------
VERSION="${NEY_VERSION:-}"
if [ -z "$VERSION" ]; then
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | head -n 1 \
    | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')" || true
fi
[ -n "$VERSION" ] || die "Could not determine the latest version (set NEY_VERSION=vX.Y.Z to pin one)."
case "$VERSION" in v*) ;; *) VERSION="v${VERSION}" ;; esac

FILENAME="ney_${VERSION#v}_${os}_${arch}.tar.gz"
CHECKSUMS="ney_${VERSION#v}_checksums.txt"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"

# State before installing: a finished setup and any ney already on PATH.
SETUP_DONE=""
[ -f "$HOME/.ney/setup-complete" ] && SETUP_DONE=1
PREV_NEY="$(command -v ney 2>/dev/null || true)"

say "Installing ney ${VERSION} (${os}/${arch}) to ${TARGET}..."

# --- download + verify ----------------------------------------------------
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Download to disk rather than piping into tar: there is nothing to hash in a
# pipe, and an unverified binary is not worth installing.
curl -fsSL "${BASE_URL}/${FILENAME}"  -o "$TMP/$FILENAME"
curl -fsSL "${BASE_URL}/${CHECKSUMS}" -o "$TMP/$CHECKSUMS"

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$TMP/$FILENAME" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$TMP/$FILENAME" | awk '{print $1}')"
else
  die "Neither sha256sum nor shasum is available — cannot verify the download.
Download manually from: https://github.com/${REPO}/releases/tag/${VERSION}"
fi

# goreleaser writes "<sha>  <file>"; some tools use a "*<file>" marker.
expected="$(awk -v f="$FILENAME" '$2 == f || $2 == "*" f {print $1}' "$TMP/$CHECKSUMS" || true)"
[ -n "$expected" ] || die "No checksum entry for ${FILENAME} in ${CHECKSUMS}."
if [ "$actual" != "$expected" ]; then
  die "CHECKSUM MISMATCH for ${FILENAME} — refusing to install.
  expected: $expected
  actual:   $actual"
fi

tar -xzf "$TMP/$FILENAME" -C "$TMP"
[ -f "$TMP/ney" ] || die "Archive ${FILENAME} has no ney binary."

# --- install --------------------------------------------------------------
# The default dir is under $HOME, so sudo only comes into play for an explicit
# NEY_INSTALL_DIR the user cannot write.
SUDO=""
if ! { mkdir -p "$INSTALL_DIR" 2>/dev/null && [ -w "$INSTALL_DIR" ]; }; then
  say "$INSTALL_DIR is not writable; using sudo."
  SUDO="sudo"
  $SUDO mkdir -p "$INSTALL_DIR"
fi
# Write beside the target, then rename: a running `ney mcp` keeps its old
# inode, and macOS never sees a half-written, re-signed binary in place.
$SUDO install -m 755 "$TMP/ney" "$INSTALL_DIR/.ney.new.$$"
$SUDO mv -f "$INSTALL_DIR/.ney.new.$$" "$TARGET"

say "ney ${VERSION} installed to ${TARGET} (checksum verified)."

# --- PATH -----------------------------------------------------------------
path_has_dir() {
  case ":$PATH:" in *":$1:"*) return 0 ;; esac
  return 1
}

rc_file() {
  case "$(basename "${SHELL:-sh}")" in
    zsh)  printf '%s\n' "${ZDOTDIR:-$HOME}/.zshrc" ;;
    bash) if [ "$os" = "darwin" ]; then printf '%s\n' "$HOME/.bash_profile"; else printf '%s\n' "$HOME/.bashrc"; fi ;;
    fish) printf '%s\n' "${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/ney.fish" ;;
    *)    printf '%s\n' "$HOME/.profile" ;;
  esac
}

ON_PATH=""
path_has_dir "$INSTALL_DIR" && ON_PATH=1
PATH_NOTE=""
if [ -z "$ON_PATH" ]; then
  rc="$(rc_file)"
  case "$rc" in
    *.fish) line="fish_add_path \"$INSTALL_DIR\"" ;;
    *)      line="export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
  esac
  if [ -n "${NEY_NO_MODIFY_PATH:-}" ]; then
    PATH_NOTE="$INSTALL_DIR is not on your PATH. Add this to your shell config:
  $line"
  elif grep -qsF "$line" "$rc" 2>/dev/null; then
    PATH_NOTE="Open a new terminal (or run: . \"$rc\") to put ney on your PATH."
  else
    mkdir -p "$(dirname "$rc")"
    printf '\n# added by the ney installer\n%s\n' "$line" >> "$rc"
    PATH_NOTE="Added $INSTALL_DIR to PATH in $rc. Open a new terminal to use \`ney\`."
  fi
fi

# --- an older ney elsewhere -----------------------------------------------
# A previous install (/usr/local/bin, Homebrew, go install) would either
# shadow this one or stay registered in your AI clients' configs.
OTHER_NEY=""
if [ -n "$PREV_NEY" ] && ! [ "$PREV_NEY" -ef "$TARGET" ]; then
  OTHER_NEY="$PREV_NEY"
fi

[ -n "$PATH_NOTE" ] && say "" && say "$PATH_NOTE"
if [ -n "$OTHER_NEY" ]; then
  warn ""
  warn "Another ney is installed at $OTHER_NEY. Remove it so it can't shadow this one:"
  if command -v brew >/dev/null 2>&1 && brew list ney >/dev/null 2>&1; then
    warn "  brew uninstall ney"
  else
    warn "  rm \"$OTHER_NEY\""
  fi
fi

# --- setup ----------------------------------------------------------------
# First install, or moving from another location (clients still point at the
# old binary): run `ney init`. A plain update needs nothing more.
NEED_INIT=""
if [ -z "$SETUP_DONE" ] || [ -n "$OTHER_NEY" ]; then
  NEED_INIT=1
fi

if [ -z "$NEED_INIT" ]; then
  # Keep ney's skills (ney-handoff, ney-resume) in step with the new binary — only where
  # they are already installed; never installs anything unasked.
  "$TARGET" skills sync --installed-only --quiet || true
  say ""
  say "Updated. Your AI clients keep using ${TARGET}; nothing else to do."
  exit 0
fi

if [ -n "${NEY_NO_INIT:-}" ]; then
  say ""
  say "Next: run  ney init  to connect Claude Code / Codex."
  exit 0
fi

# In the `curl | sh` case this script owns stdin, so hand ney init the
# terminal via /dev/tty — it refuses to run without a TTY. The wizard confirms
# every change itself, so there is no extra "run it now?" prompt here.
if [ -t 1 ] && (exec </dev/tty) 2>/dev/null; then
  say ""
  if [ -n "$SETUP_DONE" ]; then
    say "Re-running setup so your AI clients use ${TARGET}..."
  else
    say "Starting setup..."
  fi
  say ""
  exec "$TARGET" init </dev/tty
fi

say ""
say "No terminal available for the setup wizard. Next: run  ney init"
