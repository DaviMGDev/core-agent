//go:build !wasip1

package chathistory

import _ "embed"

// Wasm is the compiled guest committed beside the plugin. Rebuild it with
// cmd/build-plugins.sh chat-history.
//
//go:embed chat-history.wasm
var Wasm []byte
