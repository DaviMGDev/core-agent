//go:build !wasip1

package contextmanager

import _ "embed"

// Wasm is the compiled guest committed beside the plugin. Rebuild it with
// cmd/build-plugins.sh context-manager.
//
//go:embed context-manager.wasm
var Wasm []byte
