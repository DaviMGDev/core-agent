package link

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestDecodeRequestExamples pins the inbound vocabulary: the two requests
// decode, and everything else is refused as malformed.
func TestDecodeRequestExamples(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		want    Request
		wantErr bool
	}{
		{
			name: "deliver",
			line: `{"kind":"deliver","chat":"c1","text":"hello"}`,
			want: Request{Kind: KindDeliver, Chat: "c1", Text: "hello"},
		},
		{
			name: "load",
			line: `{"kind":"load","chat":"c1"}`,
			want: Request{Kind: KindLoad, Chat: "c1"},
		},
		{name: "unknown kind", line: `{"kind":"poke","chat":"c1"}`, wantErr: true},
		{name: "load without chat", line: `{"kind":"load"}`, wantErr: true},
		{name: "deliver without chat", line: `{"kind":"deliver","text":"hi"}`, wantErr: true},
		{name: "deliver without text", line: `{"kind":"deliver","chat":"c1"}`, wantErr: true},
		{name: "not an object", line: `you> hello`, wantErr: true},
		{name: "blank line", line: ``, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeRequest([]byte(tt.line))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("DecodeRequest(%q) accepted a malformed line: %+v", tt.line, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeRequest(%q): %v", tt.line, err)
			}
			if got != tt.want {
				t.Errorf("DecodeRequest(%q) = %+v, want %+v", tt.line, got, tt.want)
			}
		})
	}
}

// TestEncodeLineRoundTripsEachKind encodes one example of every outbound
// shape and reads it back: exactly one line per message, identical fields.
func TestEncodeLineRoundTripsEachKind(t *testing.T) {
	tests := []struct {
		name string
		v    any
	}{
		{
			name: "message",
			v:    Message{Kind: KindMessage, Chat: "c1", Role: "assistant", Text: "line one\nline two"},
		},
		{
			name: "loaded",
			v: Loaded{Kind: KindLoaded, Chat: "c1", Messages: []Turn{
				{Role: "user", Text: "hi"},
				{Role: "assistant", Text: "hey"},
			}},
		},
		{
			name: "job",
			v:    Job{Kind: KindJob, Event: "completed", Job: "7", Tool: "subagent", Detail: "the count is 3"},
		},
		{
			name: "failure",
			v:    Failure{Kind: KindError, Error: "link: deliver: empty text"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line, err := EncodeLine(tt.v)
			if err != nil {
				t.Fatalf("EncodeLine(%+v): %v", tt.v, err)
			}
			if len(line) == 0 || line[len(line)-1] != '\n' {
				t.Fatalf("line %q is not newline-terminated", line)
			}
			if n := strings.Count(string(line), "\n"); n != 1 {
				t.Fatalf("line carries %d newlines, want exactly 1: %q", n, line)
			}
			fresh := reflect.New(reflect.TypeOf(tt.v))
			if err := json.Unmarshal(line, fresh.Interface()); err != nil {
				t.Fatalf("json.Unmarshal(%q): %v", line, err)
			}
			if got := fresh.Elem().Interface(); !reflect.DeepEqual(got, tt.v) {
				t.Errorf("round trip = %#v, want %#v", got, tt.v)
			}
		})
	}
}

// TestEncodedKindFields pins the wire strings the screen switches on.
func TestEncodedKindFields(t *testing.T) {
	tests := []struct {
		v    any
		want string
	}{
		{Message{Kind: KindMessage}, string(KindMessage)},
		{Loaded{Kind: KindLoaded}, string(KindLoaded)},
		{Job{Kind: KindJob}, string(KindJob)},
		{Failure{Kind: KindError}, string(KindError)},
	}
	for _, tt := range tests {
		line, err := EncodeLine(tt.v)
		if err != nil {
			t.Fatalf("EncodeLine(%+v): %v", tt.v, err)
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(line, &doc); err != nil {
			t.Fatalf("json.Unmarshal(%q): %v", line, err)
		}
		if got := string(doc["kind"]); got != `"`+tt.want+`"` {
			t.Errorf("kind on the wire = %s, want %q", got, tt.want)
		}
	}
}
