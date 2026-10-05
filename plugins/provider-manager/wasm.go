//go:build !wasip1

package providermanager

import _ "embed"

// Wasm is the compiled guest committed beside the plugin. Rebuild it with
// cmd/build-plugins.sh provider-manager.
//
//go:embed provider-manager.wasm
var Wasm []byte
