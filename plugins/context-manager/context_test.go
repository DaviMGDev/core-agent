package contextmanager

import (
	"reflect"
	"testing"
)

func texts(msgs []Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Text
	}
	return out
}

func TestProjectExactFitKeepsAll(t *testing.T) {
	history := []Message{{Role: "user", Text: "aa"}, {Role: "assistant", Text: "bb"}}
	w := Project(history, 4)
	if got := texts(w.Messages); !reflect.DeepEqual(got, []string{"aa", "bb"}) {
		t.Fatalf("Messages = %v, want [aa bb]", got)
	}
	if w.Dropped != 0 {
		t.Fatalf("Dropped = %d, want 0", w.Dropped)
	}
}

func TestProjectOverBudgetDropsOldest(t *testing.T) {
	history := []Message{{Text: "aaa"}, {Text: "bbb"}, {Text: "cc"}}
	w := Project(history, 5)
	if got := texts(w.Messages); !reflect.DeepEqual(got, []string{"bbb", "cc"}) {
		t.Fatalf("Messages = %v, want [bbb cc]", got)
	}
	if w.Dropped != 1 {
		t.Fatalf("Dropped = %d, want 1", w.Dropped)
	}
}

func TestProjectKeepsOversizedNewest(t *testing.T) {
	history := []Message{{Text: "old"}, {Text: "very long newest"}}
	w := Project(history, 4)
	if got := texts(w.Messages); !reflect.DeepEqual(got, []string{"very long newest"}) {
		t.Fatalf("Messages = %v, want [very long newest]", got)
	}
	if w.Dropped != 1 {
		t.Fatalf("Dropped = %d, want 1", w.Dropped)
	}
}

func TestProjectEmptyHistory(t *testing.T) {
	w := Project(nil, 100)
	if len(w.Messages) != 0 || w.Dropped != 0 {
		t.Fatalf("Project(nil) = %+v, want empty window", w)
	}
}

func TestProjectNonPositiveBudgetKeepsNewest(t *testing.T) {
	history := []Message{{Text: "old"}, {Text: "newest"}}
	for _, budget := range []int{0, -5} {
		w := Project(history, budget)
		if got := texts(w.Messages); !reflect.DeepEqual(got, []string{"newest"}) {
			t.Fatalf("budget %d: Messages = %v, want [newest]", budget, got)
		}
		if w.Dropped != 1 {
			t.Fatalf("budget %d: Dropped = %d, want 1", budget, w.Dropped)
		}
	}
}

func TestProjectCountsRunesNotBytes(t *testing.T) {
	history := []Message{{Text: "héllo"}} // 5 runes, 6 bytes
	w := Project(history, 5)
	if len(w.Messages) != 1 || w.Dropped != 0 {
		t.Fatalf("Project = %+v, want the 5-rune message kept", w)
	}
	if w2 := Project(history, 4); len(w2.Messages) != 1 || w2.Dropped != 0 {
		t.Fatalf("Project(budget 4) = %+v, want newest kept anyway", w2)
	}
}

func TestParseConfig(t *testing.T) {
	c, err := ParseConfig([]byte(`{"budget":2048}`))
	if err != nil || c.Budget != 2048 {
		t.Fatalf("ParseConfig = %+v, %v; want budget 2048", c, err)
	}
	if c, err = ParseConfig(nil); err != nil || c.Budget != 0 {
		t.Fatalf("ParseConfig(nil) = %+v, %v; want zero", c, err)
	}
	if _, err = ParseConfig([]byte("nope")); err == nil {
		t.Fatal("ParseConfig(invalid) = nil, want error")
	}
}
