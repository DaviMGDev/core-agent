#!/usr/bin/env bash
# Visual smoke setup: a scripted provider + nvchat in a real terminal.
#
# After this script returns, the session is live and listening on
# $SOCK. Drive it with nvim --server, then screenshot, e.g.
#   nvim --server /tmp/nvchat.sock --remote-send 'delegate the count<CR>'
#   pkill -f nvchat-core-agent      # freeze the screen (4.3)
set -euo pipefail

cd "$(dirname "$0")/../.."

CORE=/tmp/nvchat-core-agent
SMOKE=/tmp/nvchat-smoke
SOCK=/tmp/nvchat.sock

mkdir -p "$SMOKE"
go build -o "$CORE" ./cmd/core-agent

# A provider document pointing at the local stub and a view that uses it.
cat > "$SMOKE/providers.json" <<'JSON'
{"providers":[{"name":"stub","endpoint":"http://127.0.0.1:8099","models":["stub-model"]}]}
JSON
cat > "$SMOKE/models.json" <<'JSON'
{"models":[{"name":"fast","alias":"stub-model"},{"name":"reliable","fallback":["stub-model"]}]}
JSON

if ! pgrep -f smoke_provider.py > /dev/null; then
  python3 tui/tests/smoke_provider.py > "$SMOKE/stub.log" 2>&1 &
  sleep 0.5
fi

rm -f "$SOCK"
setsid --fork env CORE_DIR="$SMOKE" NVIM_APPNAME=nvchat alacritty \
  --working-directory "$PWD" \
  -t nvchat-smoke -e \
  nvim -u "$PWD/tui/init.lua" \
  --cmd "set rtp^=$PWD/tui" \
  --cmd "let g:nvchat_core='$CORE'" \
  --listen "$SOCK" > /dev/null 2>&1

for _ in $(seq 1 60); do
  if [ -S "$SOCK" ]; then
    echo "nvchat is listening on $SOCK"
    exit 0
  fi
  sleep 1
done
echo "nvchat did not start its socket (see $SMOKE/stub.log)" >&2
exit 1
