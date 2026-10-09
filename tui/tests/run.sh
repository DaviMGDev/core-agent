#!/usr/bin/env bash
# Scripted checks for the core-backed nvchat store (plan leaves 3.1-3.5).
#
# Builds core-agent, proves only store.lua knows the core exists, then runs
# the headless Neovim script against the real link with the mock provider.
set -euo pipefail

cd "$(dirname "$0")/../.."

CORE=/tmp/nvchat-core-agent
go build -o "$CORE" ./cmd/core-agent

# 3.1: the store module is the only Lua code that knows the core exists.
leaked=$(grep -lE 'jobstart|chansend|nvchat_core|core-agent' tui/lua/nvchat/*.lua | grep -v 'nvchat/store.lua' || true)
if [ -n "$leaked" ]; then
  echo "FAIL: core knowledge leaked outside store.lua: $leaked"
  exit 1
fi
echo "ok: only store.lua mentions the core"

timeout 300 nvim --headless -u NONE \
  -c "let g:nvchat_core='$CORE'" \
  -c "let g:nvchat_core_args=['-mock']" \
  -c "luafile tui/tests/store_test.lua"

timeout 300 nvim --headless -u NONE \
  -c "let g:nvchat_core='$CORE'" \
  -c "let g:nvchat_core_args=['-mock']" \
  -c "luafile tui/tests/ui_test.lua"
