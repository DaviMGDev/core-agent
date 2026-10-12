package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGuestInvocationResponseBufferOversizeHandling(t *testing.T) {
	// 1. Data within limit round-trips intact
	smallData := []byte(`{"text":"hello world"}`)
	buf1 := make([]byte, 1024)
	n1 := WriteResponseOrOversize(buf1, 1024, smallData)
	if n1 != uint32(len(smallData)) {
		t.Fatalf("n1 = %d, want %d", n1, len(smallData))
	}
	if string(buf1[:n1]) != string(smallData) {
		t.Fatalf("got = %q, want %q", string(buf1[:n1]), string(smallData))
	}

	// 2. Data exceeding buffer limit returns structured error naming the limit instead of empty body
	largeData := []byte(strings.Repeat("a", 2000))
	buf2 := make([]byte, 500)
	n2 := WriteResponseOrOversize(buf2, 500, largeData)
	if n2 == 0 {
		t.Fatal("WriteResponseOrOversize returned 0, want structured error")
	}
	var errDoc struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(buf2[:n2], &errDoc); err != nil {
		t.Fatalf("unmarshal oversize error: %v, body = %s", err, string(buf2[:n2]))
	}
	if !strings.Contains(errDoc.Error, "response oversize") || !strings.Contains(errDoc.Error, "500") {
		t.Fatalf("errDoc.Error = %q, want naming the limit 500", errDoc.Error)
	}

	// 3. Dynamic sizing / re-read pattern: caller accommodates large payload
	buf3 := make([]byte, 65536)
	n3 := WriteResponseOrOversize(buf3, 65536, largeData)
	if n3 != uint32(len(largeData)) {
		t.Fatalf("n3 = %d, want %d with expanded buffer", n3, len(largeData))
	}
	if string(buf3[:n3]) != string(largeData) {
		t.Fatalf("dynamic buffer round-trip failed")
	}
}
