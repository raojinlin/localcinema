#!/bin/sh
set -e

REPO="raojinlin/localcinema"
BINARY="localcinema"

detect_os() {
  case "$(uname -s)" in
    Darwin) echo "darwin" ;;
    Linux)  echo "linux" ;;
    *)      echo "Unsupported OS" >&2; exit 1 ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)  echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *)             echo "Unsupported architecture" >&2; exit 1 ;;
  esac
}

OS=$(detect_os)
ARCH=$(detect_arch)

echo "Detected: ${OS}/${ARCH}"

# Get latest version
VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
if [ -z "$VERSION" ]; then
  echo "Failed to fetch latest version" >&2
  exit 1
fi
echo "Latest version: ${VERSION}"

# Determine archive extension
EXT="tar.gz"
if [ "$OS" = "darwin" ]; then
  EXT="zip"
fi

URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY}_${VERSION#v}_${OS}_${ARCH}.${EXT}"
echo "Downloading ${URL}..."

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

curl -fsSL -o "${TMP}/archive.${EXT}" "$URL"

# Extract
if [ "$EXT" = "zip" ]; then
  unzip -q "${TMP}/archive.zip" -d "$TMP"
else
  tar -xzf "${TMP}/archive.tar.gz" -C "$TMP"
fi

# Install
INSTALL_DIR="/usr/local/bin"
if [ ! -w "$INSTALL_DIR" ]; then
  INSTALL_DIR="${HOME}/.local/bin"
  mkdir -p "$INSTALL_DIR"
  echo "No write access to /usr/local/bin, installing to ${INSTALL_DIR}"
  echo "Make sure ${INSTALL_DIR} is in your PATH"
fi

cp "${TMP}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
chmod +x "${INSTALL_DIR}/${BINARY}"

echo "Installed ${BINARY} ${VERSION} to ${INSTALL_DIR}/${BINARY}"
