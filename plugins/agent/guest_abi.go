package agent

import "fmt"

// OversizeResponseCap is the default guest response buffer limit (64 KiB).
const OversizeResponseCap uint32 = 65536

// WriteResponseOrOversize writes data into buf up to max bytes.
// If len(data) exceeds max, it formats a structured JSON error naming the limit
// (e.g. {"error":"response oversize: N bytes exceeds cap M"}) instead of returning
// an empty body or failing with unexpected end of JSON input.
func WriteResponseOrOversize(buf []byte, max uint32, data []byte) uint32 {
	if uint32(len(data)) <= max {
		copy(buf[:max], data)
		return uint32(len(data))
	}
	errDoc := []byte(fmt.Sprintf(`{"error":"response oversize: %d bytes exceeds cap %d"}`, len(data), max))
	if uint32(len(errDoc)) <= max {
		copy(buf[:max], errDoc)
		return uint32(len(errDoc))
	}
	return 0
}
