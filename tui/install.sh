#!/usr/bin/env bash
# install.sh - Install the nvchat wrapper script
#
# Usage: ./install.sh [target-dir]
#
# If no target-dir is given, it defaults to ~/.local/bin (which is
# typically in PATH on modern Linux/macOS setups).

set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT_NAME="nvchat"
SCRIPT_SRC="${REPO_DIR}/bin/${SCRIPT_NAME}"

# Target directory: argument or default
TARGET_DIR="${1:-${HOME}/.local/bin}"

# Resolve to absolute path
TARGET_DIR="$(cd "$(dirname "${TARGET_DIR}/.")" && pwd)"

echo "📦 Installing ${SCRIPT_NAME}..."

# Create target directory if needed
if [ ! -d "${TARGET_DIR}" ]; then
    echo "   Creating directory: ${TARGET_DIR}"
    mkdir -p "${TARGET_DIR}"
fi

# Copy the script
cp "${SCRIPT_SRC}" "${TARGET_DIR}/${SCRIPT_NAME}"
chmod +x "${TARGET_DIR}/${SCRIPT_NAME}"

echo "   ✓ Copied to: ${TARGET_DIR}/${SCRIPT_NAME}"

# Warn if target not in PATH
case ":${PATH}:" in
    *:"${TARGET_DIR}":*)
        ;;
    *)
        echo "   ⚠  Warning: ${TARGET_DIR} is not in your PATH."
        echo "      Add the following line to your shell config (~/.bashrc, ~/.zshrc, etc.):"
        echo "      export PATH=\"\${PATH}:${TARGET_DIR}\""
        ;;
esac

echo "   ✅ Done! You can now run: nvchat"
