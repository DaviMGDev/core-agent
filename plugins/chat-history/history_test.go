package chathistory

import (
	"reflect"
	"sync"
	"testing"
)

func TestAppendRecordsInOrder(t *testing.T) {
	s := NewStore()
	if _, err := s.Append("main", RoleUser, "hello"); err != nil {
		t.Fatalf("Append(user): %v", err)
	}
	if _, err := s.Append("main", RoleAssistant, "hi"); err != nil {
		t.Fatalf("Append(assistant): %v", err)
	}
	got := s.Messages("main")
	want := []Message{{RoleUser, "hello"}, {RoleAssistant, "hi"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Messages = %v, want %v", got, want)
	}
}

func TestAppendRefusesUnknownRole(t *testing.T) {
	s := NewStore()
	if _, err := s.Append("main", "system", "x"); err == nil {
		t.Fatal("Append(system) = nil, want error")
	}
	if s.Len("main") != 0 {
		t.Fatalf("Len = %d after refused append, want 0", s.Len("main"))
	}
}

func TestAppendStartsUnknownConversation(t *testing.T) {
	s := NewStore()
	if _, err := s.Append("main", RoleUser, "hello"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if got := s.Conversations(); !reflect.DeepEqual(got, []string{"main"}) {
		t.Fatalf("Conversations = %v, want [main]", got)
	}
}

func TestRecentWindow(t *testing.T) {
	s := NewStore()
	for _, text := range []string{"one", "two", "three"} {
		if _, err := s.Append("main", RoleUser, text); err != nil {
			t.Fatalf("Append(%s): %v", text, err)
		}
	}
	got := s.Recent("main", 2)
	want := []Message{{RoleUser, "two"}, {RoleUser, "three"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Recent(2) = %v, want %v", got, want)
	}
	if got := s.Recent("main", 5); len(got) != 3 {
		t.Fatalf("Recent(5) = %v, want all 3", got)
	}
	if got := s.Recent("main", 0); got != nil {
		t.Fatalf("Recent(0) = %v, want none", got)
	}
	if got := s.Recent("missing", 2); got != nil {
		t.Fatalf("Recent(missing) = %v, want none", got)
	}
}

func TestConversationsInCreationOrder(t *testing.T) {
	s := NewStore()
	for _, id := range []string{"main", "side"} {
		if err := s.Start(id); err != nil {
			t.Fatalf("Start(%s): %v", id, err)
		}
	}
	if got := s.Conversations(); !reflect.DeepEqual(got, []string{"main", "side"}) {
		t.Fatalf("Conversations = %v, want [main side]", got)
	}
	if err := s.Start("main"); err != nil {
		t.Fatalf("Start(main) again: %v", err)
	}
	if got := s.Conversations(); !reflect.DeepEqual(got, []string{"main", "side"}) {
		t.Fatalf("Conversations after restart = %v, want [main side]", got)
	}
}

func TestMessagesAreCopies(t *testing.T) {
	s := NewStore()
	if _, err := s.Append("main", RoleUser, "hello"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got := s.Messages("main")
	got[0].Text = "mutated"
	if again := s.Messages("main"); again[0].Text != "hello" {
		t.Fatalf("store mutated through returned slice: %v", again)
	}
}

func TestConcurrentAppends(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Append("main", RoleUser, "x"); err != nil {
				t.Errorf("Append: %v", err)
			}
		}()
	}
	wg.Wait()
	if n := s.Len("main"); n != 50 {
		t.Fatalf("Len = %d after concurrent appends, want 50", n)
	}
}
