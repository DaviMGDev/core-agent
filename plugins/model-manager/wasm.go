//go:build !wasip1

package modelmanager

import _ "embed"

// Wasm is the compiled guest committed beside the plugin. Rebuild it with
// cmd/build-plugins.sh model-manager.
//
//go:embed model-manager.wasm
var Wasm []byte
