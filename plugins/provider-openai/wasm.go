//go:build !wasip1

package provideropenai

import _ "embed"

// Wasm is the compiled guest committed beside the plugin. Rebuild it with
// cmd/build-plugins.sh provider-openai.
//
//go:embed provider-openai.wasm
var Wasm []byte
