//go:build !wasip1

package agent

import _ "embed"

// Wasm is the compiled guest committed beside the plugin. Rebuild it with
// cmd/build-plugins.sh agent.
//
//go:embed agent.wasm
var Wasm []byte
