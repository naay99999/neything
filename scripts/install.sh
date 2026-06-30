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
    echo "Unsupported OS: $OS"
    exit 1
    ;;
esac

# detect arch
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

# fetch latest version tag
VERSION="$(curl -sSf "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' \
  | sed 's/.*"tag_name": *"\(.*\)".*/\1/')"

if [ -z "$VERSION" ]; then
  echo "Could not determine latest version"
  exit 1
fi

FILENAME="ney_${VERSION#v}_${os}_${arch}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${FILENAME}"

echo "Installing ney ${VERSION} (${os}/${arch})..."

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

curl -sSfL "$URL" | tar -xz -C "$TMP"

if [ ! -w "$INSTALL_DIR" ]; then
  echo "Installing to $INSTALL_DIR (needs sudo)..."
  sudo install -m755 "$TMP/ney" "$INSTALL_DIR/ney"
else
  install -m755 "$TMP/ney" "$INSTALL_DIR/ney"
fi

echo "ney ${VERSION} installed to ${INSTALL_DIR}/ney"
echo "Run: ney version"
