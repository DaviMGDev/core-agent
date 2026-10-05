package modelmanager

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestValidateOneModeRule(t *testing.T) {
	cases := []struct {
		name string
		v    View
	}{
		{"no mode", View{Name: "x"}},
		{"two modes", View{Name: "x", Alias: "a", Fallback: []string{"b"}}},
		{"three modes", View{Name: "x", Alias: "a", Fallback: []string{"b"}, Discuss: []string{"c"}}},
		{"empty name", View{Name: " ", Alias: "a"}},
		{"empty target", View{Name: "x", Fallback: []string{"a", ""}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Validate(c.v); err == nil {
				t.Fatalf("Validate(%+v) = nil, want error", c.v)
			}
			r := NewRegistry()
			if err := r.Register(c.v); err == nil {
				t.Fatalf("Register(%+v) = nil, want error", c.v)
			}
			if r.Len() != 0 {
				t.Fatalf("registry changed on refused view: Len() = %d", r.Len())
			}
		})
	}
}

func TestDuplicateViewRefused(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(View{Name: "fast", Alias: "gpt"}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register(View{Name: "fast", Alias: "other"}); err == nil {
		t.Fatal("duplicate Register = nil, want error")
	}
	if r.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", r.Len())
	}
}

func mustResolve(t *testing.T, r *Registry, name string) Resolution {
	t.Helper()
	res, err := r.Resolve(name)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", name, err)
	}
	return res
}

func TestResolvePlainModel(t *testing.T) {
	res := mustResolve(t, NewRegistry(), "gpt")
	if res.Mode != ModeModel {
		t.Fatalf("Mode = %q, want %q", res.Mode, ModeModel)
	}
	if !reflect.DeepEqual(res.Models, []string{"gpt"}) {
		t.Fatalf("Models = %v, want [gpt]", res.Models)
	}
}

func TestResolveAlias(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(View{Name: "fast", Alias: "gpt"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res := mustResolve(t, r, "fast")
	if res.Mode != ModeAlias {
		t.Fatalf("Mode = %q, want %q", res.Mode, ModeAlias)
	}
	if !reflect.DeepEqual(res.Models, []string{"gpt"}) {
		t.Fatalf("Models = %v, want [gpt]", res.Models)
	}
}

func TestResolveFallbackOrder(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(View{Name: "reliable", Fallback: []string{"a", "b"}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res := mustResolve(t, r, "reliable")
	if res.Mode != ModeFallback {
		t.Fatalf("Mode = %q, want %q", res.Mode, ModeFallback)
	}
	if !reflect.DeepEqual(res.Models, []string{"a", "b"}) {
		t.Fatalf("Models = %v, want [a b]", res.Models)
	}
}

func TestResolveDiscussOrder(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(View{Name: "panel", Discuss: []string{"a", "b", "c"}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res := mustResolve(t, r, "panel")
	if res.Mode != ModeDiscuss {
		t.Fatalf("Mode = %q, want %q", res.Mode, ModeDiscuss)
	}
	if !reflect.DeepEqual(res.Models, []string{"a", "b", "c"}) {
		t.Fatalf("Models = %v, want [a b c]", res.Models)
	}
}

func TestNestedViewsFlatten(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(View{Name: "fast", Alias: "gpt"}); err != nil {
		t.Fatalf("Register(fast): %v", err)
	}
	if err := r.Register(View{Name: "reliable", Fallback: []string{"fast", "local"}}); err != nil {
		t.Fatalf("Register(reliable): %v", err)
	}
	res := mustResolve(t, r, "reliable")
	if !reflect.DeepEqual(res.Models, []string{"gpt", "local"}) {
		t.Fatalf("Models = %v, want [gpt local]", res.Models)
	}
}

func TestResolveRefusesCycles(t *testing.T) {
	self := NewRegistry()
	if err := self.Register(View{Name: "loop", Alias: "loop"}); err != nil {
		t.Fatalf("Register(loop): %v", err)
	}
	if _, err := self.Resolve("loop"); err == nil {
		t.Fatal("self-reference resolved without error")
	}

	mutual := NewRegistry()
	if err := mutual.Register(View{Name: "a", Alias: "b"}); err != nil {
		t.Fatalf("Register(a): %v", err)
	}
	if err := mutual.Register(View{Name: "b", Alias: "a"}); err != nil {
		t.Fatalf("Register(b): %v", err)
	}
	if _, err := mutual.Resolve("a"); err == nil {
		t.Fatal("mutual alias cycle resolved without error")
	}
}

func TestParseConfig(t *testing.T) {
	payload := []byte(`{"models":[{"name":"fast","alias":"llama"},{"name":"reliable","fallback":["llama","gpt"]},{"name":"panel","discuss":["llama","gpt"]}]}`)
	views, err := ParseConfig(payload)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(views) != 3 {
		t.Fatalf("parsed %d views, want 3", len(views))
	}
	if views[0].Name != "fast" || views[1].Name != "reliable" || views[2].Name != "panel" {
		t.Fatalf("parsed order = %q, %q, %q", views[0].Name, views[1].Name, views[2].Name)
	}
	if _, err := ParseConfig([]byte("not json")); err == nil {
		t.Fatal("ParseConfig(invalid) = nil, want error")
	}
	views, err = ParseConfig(nil)
	if err != nil || len(views) != 0 {
		t.Fatalf("ParseConfig(nil) = %v, %v; want empty", views, err)
	}
}

// stubCaller records the models it was asked for and answers deterministically.
type stubCaller struct {
	calls  []string
	failOn map[string]bool
}

func (c *stubCaller) Complete(model string, messages []ContextMessage) (string, error) {
	c.calls = append(c.calls, model)
	if c.failOn[model] {
		return "", errors.New("call failed: " + model)
	}
	return fmt.Sprintf("%s(%d)", model, len(messages)), nil
}

func TestApplyOperations(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(View{Name: "fast", Alias: "llama"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	res, err := Apply(r, Op{Kind: "resolve", Name: "fast"}, nil)
	if err != nil {
		t.Fatalf("Apply(resolve): %v", err)
	}
	if res.Mode != ModeAlias || !reflect.DeepEqual(res.Models, []string{"llama"}) {
		t.Fatalf("resolve = %+v, want alias [llama]", res)
	}

	caller := &stubCaller{}
	res, err = Apply(r, Op{Kind: "respond", Model: "fast", Text: "hello"}, caller)
	if err != nil {
		t.Fatalf("Apply(respond): %v", err)
	}
	if res.Model != "llama" || res.Text != "llama(0)" {
		t.Fatalf("respond = %+v, want the caller's answer for llama", res)
	}

	withContext, err := Apply(r, Op{Kind: "respond", Model: "fast", Context: []ContextMessage{{Role: "user", Text: "x"}}}, caller)
	if err != nil {
		t.Fatalf("Apply(respond with context): %v", err)
	}
	if withContext.Text != "llama(1)" {
		t.Fatalf("respond with context = %+v, want the caller to see one message", withContext)
	}

	if _, err := Apply(r, Op{Kind: "respond", Model: "fast"}, nil); err == nil {
		t.Fatal("Apply(respond without caller) = nil, want error")
	}
	if _, err := Apply(r, Op{Kind: "sing"}, caller); err == nil {
		t.Fatal("Apply(unknown) = nil, want error")
	}
}

func TestApplyFallbackTriesTargetsInOrder(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(View{Name: "reliable", Fallback: []string{"a", "b"}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	caller := &stubCaller{failOn: map[string]bool{"a": true}}

	res, err := Apply(r, Op{Kind: "respond", Model: "reliable"}, caller)
	if err != nil {
		t.Fatalf("Apply(respond): %v", err)
	}
	if res.Model != "b" || !reflect.DeepEqual(caller.calls, []string{"a", "b"}) {
		t.Fatalf("respond = %+v with calls %v, want b after trying a then b", res, caller.calls)
	}
}

func TestApplyFallbackExhaustionFails(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(View{Name: "reliable", Fallback: []string{"a", "b"}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	caller := &stubCaller{failOn: map[string]bool{"a": true, "b": true}}

	if _, err := Apply(r, Op{Kind: "respond", Model: "reliable"}, caller); err == nil {
		t.Fatal("Apply(respond) = nil, want an exhaustion error")
	}
}
