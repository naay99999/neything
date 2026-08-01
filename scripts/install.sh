#!/usr/bin/env sh
set -e

REPO="naay99999/neything"
INSTALL_DIR="${NEY_INSTALL_DIR:-/usr/local/bin}"

# detect OS
OS="$(uname -s)"
case "$OS" in
  Linux)  os="linux"  ;;
  Darwin) os="darwin" ;;
  *)
    echo "Unsupported OS: $OS" >&2
    echo "ney ships prebuilt binaries for Linux and macOS only — there is no Windows build." >&2
    echo "On Windows use WSL2, or build from source:" >&2
    echo "  go install github.com/naay99999/neything/cmd/ney@latest" >&2
    exit 1
    ;;
esac

# detect arch
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

# resolve version — NEY_VERSION pins, otherwise ask GitHub for the latest tag
VERSION="${NEY_VERSION:-}"
if [ -z "$VERSION" ]; then
  VERSION="$(curl -sSf "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' \
    | sed 's/.*"tag_name": *"\(.*\)".*/\1/')"
fi
if [ -z "$VERSION" ]; then
  echo "Could not determine the latest version (set NEY_VERSION=vX.Y.Z to pin one)" >&2
  exit 1
fi
case "$VERSION" in v*) ;; *) VERSION="v${VERSION}" ;; esac

FILENAME="ney_${VERSION#v}_${os}_${arch}.tar.gz"
CHECKSUMS="ney_${VERSION#v}_checksums.txt"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"

echo "Installing ney ${VERSION} (${os}/${arch})..."

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Download to disk rather than piping into tar: there is nothing to hash in a
# pipe, and an unverified binary is not worth installing.
curl -sSfL "${BASE_URL}/${FILENAME}"  -o "$TMP/$FILENAME"
curl -sSfL "${BASE_URL}/${CHECKSUMS}" -o "$TMP/$CHECKSUMS"

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$TMP/$FILENAME" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$TMP/$FILENAME" | awk '{print $1}')"
else
  echo "Neither sha256sum nor shasum is available — cannot verify the download." >&2
  echo "Install one of them, or download manually from:" >&2
  echo "  https://github.com/${REPO}/releases/tag/${VERSION}" >&2
  exit 1
fi

# goreleaser writes "<sha>  <file>"; some tools use a "*<file>" marker.
expected="$(awk -v f="$FILENAME" '$2 == f || $2 == "*" f {print $1}' "$TMP/$CHECKSUMS" || true)"
if [ -z "$expected" ]; then
  echo "No checksum entry for ${FILENAME} in ${CHECKSUMS}" >&2
  exit 1
fi
if [ "$actual" != "$expected" ]; then
  echo "CHECKSUM MISMATCH for ${FILENAME} — refusing to install." >&2
  echo "  expected: $expected" >&2
  echo "  actual:   $actual" >&2
  exit 1
fi

tar -xzf "$TMP/$FILENAME" -C "$TMP"

if [ ! -w "$INSTALL_DIR" ]; then
  echo "Installing to $INSTALL_DIR (needs sudo)..."
  sudo install -m755 "$TMP/ney" "$INSTALL_DIR/ney"
else
  install -m755 "$TMP/ney" "$INSTALL_DIR/ney"
fi

echo "ney ${VERSION} installed to ${INSTALL_DIR}/ney (checksum verified)"
echo ""
echo "Next: run  ney init  — guided setup. It finds your git repos, sets up your"
echo "profile, and connects Claude Desktop / Claude Code / Codex. Takes about a minute."

# In the `curl | sh` case this script owns stdin, so prompt and hand off via
# /dev/tty — ney init refuses to run without a TTY on stdin.
if [ -z "${NEY_NO_INIT:-}" ] && [ -t 1 ] && [ -c /dev/tty ]; then
  printf "Run it now? [Y/n] "
  if read -r ans </dev/tty 2>/dev/null; then
    case "$ans" in
      ""|y|Y|yes|Yes) exec "${INSTALL_DIR}/ney" init </dev/tty ;;
    esac
  fi
fi
