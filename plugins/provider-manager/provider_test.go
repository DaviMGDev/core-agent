package providermanager

import (
	"errors"
	"reflect"
	"testing"
)

func validProvider(name string) Provider {
	return Provider{
		Name:       name,
		Endpoint:   "https://api.example.com/v1",
		Credential: "env:EXAMPLE_KEY",
	}
}

func TestRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	p := validProvider("openai")
	if err := r.Register(p); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Get("openai")
	if !ok {
		t.Fatal("Get(openai): not found")
	}
	if !reflect.DeepEqual(got, p) {
		t.Fatalf("Get(openai) = %+v, want %+v", got, p)
	}
	if r.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", r.Len())
	}
}

func TestValidateRefusals(t *testing.T) {
	cases := []struct {
		name string
		p    Provider
	}{
		{"empty name", Provider{Name: " ", Endpoint: "https://a.example", Credential: "env:X"}},
		{"relative endpoint", Provider{Name: "a", Endpoint: "api.example.com", Credential: "env:X"}},
		{"unsupported scheme", Provider{Name: "a", Endpoint: "ftp://api.example.com", Credential: "env:X"}},
		{"empty endpoint", Provider{Name: "a", Endpoint: "", Credential: "env:X"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Validate(c.p); err == nil {
				t.Fatalf("Validate(%+v) = nil, want error", c.p)
			}
			r := NewRegistry()
			if err := r.Register(c.p); err == nil {
				t.Fatalf("Register(%+v) = nil, want error", c.p)
			}
			if r.Len() != 0 {
				t.Fatalf("registry changed on refused provider: Len() = %d", r.Len())
			}
		})
	}
}

func TestDuplicateRefused(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(validProvider("openai")); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register(validProvider("openai"))
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("second Register error = %v, want ErrDuplicate", err)
	}
	if r.Len() != 1 {
		t.Fatalf("Len() = %d after duplicate, want 1", r.Len())
	}
}

func TestListPreservesInsertionOrder(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"openai", "local", "azure"} {
		if err := r.Register(validProvider(name)); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}
	var names []string
	for _, p := range r.List() {
		names = append(names, p.Name)
	}
	want := []string{"openai", "local", "azure"}
	if len(names) != len(want) {
		t.Fatalf("List() = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("List() = %v, want %v", names, want)
		}
	}
}

func TestRemove(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"openai", "local"} {
		if err := r.Register(validProvider(name)); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}
	if err := r.Remove("openai"); err != nil {
		t.Fatalf("Remove(openai): %v", err)
	}
	if _, ok := r.Get("openai"); ok {
		t.Fatal("openai still present after Remove")
	}
	if r.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", r.Len())
	}
	if err := r.Remove("openai"); err == nil {
		t.Fatal("Remove(openai) twice = nil, want error")
	}
}

func TestParseConfig(t *testing.T) {
	payload := []byte(`{"providers":[{"name":"openai","endpoint":"https://api.openai.com/v1","credential":"env:OPENAI_API_KEY"},{"name":"local","endpoint":"http://127.0.0.1:11434/v1","credential":"env:LOCAL_KEY"}]}`)
	providers, err := ParseConfig(payload)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(providers) != 2 {
		t.Fatalf("parsed %d providers, want 2", len(providers))
	}
	if providers[0].Name != "openai" || providers[1].Name != "local" {
		t.Fatalf("parsed order = %q, %q", providers[0].Name, providers[1].Name)
	}
}

func TestParseConfigEmpty(t *testing.T) {
	providers, err := ParseConfig(nil)
	if err != nil {
		t.Fatalf("ParseConfig(nil): %v", err)
	}
	if len(providers) != 0 {
		t.Fatalf("ParseConfig(nil) = %v, want empty", providers)
	}
}

func TestParseConfigInvalid(t *testing.T) {
	if _, err := ParseConfig([]byte("not json")); err == nil {
		t.Fatal("ParseConfig(invalid) = nil, want error")
	}
}

func TestValidateRejectsEmptyModel(t *testing.T) {
	p := Provider{Name: "openai", Endpoint: "https://api.example.com/v1", Credential: "env:KEY", Models: []string{"gpt-4o-mini", " "}}
	if err := Validate(p); err == nil {
		t.Fatal("Validate with an empty model = nil, want error")
	}
}

func TestProviderFor(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Provider{Name: "local", Endpoint: "http://127.0.0.1:11434/v1", Credential: "env:LOCAL_KEY", Models: []string{"llama-3.2"}}); err != nil {
		t.Fatalf("Register(local): %v", err)
	}
	if err := r.Register(Provider{Name: "openai", Endpoint: "https://api.openai.com/v1", Credential: "env:OPENAI_API_KEY", Models: []string{"gpt-4o-mini"}}); err != nil {
		t.Fatalf("Register(openai): %v", err)
	}

	p, ok := r.ProviderFor("gpt-4o-mini")
	if !ok || p.Name != "openai" {
		t.Fatalf("ProviderFor(gpt-4o-mini) = %+v, %v; want openai", p, ok)
	}
	if _, ok := r.ProviderFor("unknown"); ok {
		t.Fatal("ProviderFor(unknown) = ok, want absent")
	}
}

func TestProviderForUsesInsertionOrder(t *testing.T) {
	r := NewRegistry()
	for _, p := range []Provider{
		{Name: "first", Endpoint: "https://a.example/v1", Credential: "env:A", Models: []string{"shared"}},
		{Name: "second", Endpoint: "https://b.example/v1", Credential: "env:B", Models: []string{"shared"}},
	} {
		if err := r.Register(p); err != nil {
			t.Fatalf("Register(%s): %v", p.Name, err)
		}
	}
	p, ok := r.ProviderFor("shared")
	if !ok || p.Name != "first" {
		t.Fatalf("ProviderFor(shared) = %+v, %v; want the first provider", p, ok)
	}
}

func TestKeylessProviderRegisters(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Provider{Name: "local", Endpoint: "http://127.0.0.1:11434/v1", Models: []string{"llama-3.2"}}); err != nil {
		t.Fatalf("Register(keyless): %v", err)
	}
	p, ok := r.Get("local")
	if !ok || p.Credential != "" {
		t.Fatalf("Get(local) = %+v, %v; want a provider with no credential", p, ok)
	}
}

func TestMockProviderNeedsNoEndpointOrCredential(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Provider{Name: "mock", Mock: true, Models: []string{"llama-3.2"}}); err != nil {
		t.Fatalf("Register(mock): %v", err)
	}
	p, ok := r.ProviderFor("llama-3.2")
	if !ok || !p.Mock || p.Name != "mock" {
		t.Fatalf("ProviderFor(llama-3.2) = %+v, %v; want the mock provider", p, ok)
	}
	if _, ok := r.ProviderFor("unserved"); ok {
		t.Fatal("ProviderFor(unserved) = ok, want absent")
	}
}
