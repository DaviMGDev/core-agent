#!/usr/bin/env sh
# Builds plugin guests into .wasm artifacts beside their plugins.
#
# Usage: cmd/build-plugins.sh [plugin...]
# Defaults to the five starter plugins. The host side embeds each artifact
# (plugins/<name>/wasm.go), so rebuild before running the tests.
set -eu
cd "$(dirname "$0")/.."
plugins="${*:-agent repl-chat provider-manager model-manager chat-history context-manager}"
for p in $plugins; do
	echo "building plugins/$p/guest -> plugins/$p/$p.wasm"
	GOOS=wasip1 GOARCH=wasm go build \
		-buildmode=c-shared \
		-trimpath -ldflags="-s -w" \
		-o "plugins/$p/$p.wasm" "./plugins/$p/guest"
done
