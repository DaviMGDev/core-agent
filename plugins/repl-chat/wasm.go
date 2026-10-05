//go:build !wasip1

package replchat

import _ "embed"

// Wasm is the compiled guest committed beside the plugin. Rebuild it with
// cmd/build-plugins.sh repl-chat.
//
//go:embed repl-chat.wasm
var Wasm []byte
