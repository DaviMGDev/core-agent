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

func TestProjectEnforcesUserFirstHeadAlignmentWhenPruned(t *testing.T) {
	history := []Message{
		{Role: "user", Text: "aaaa"},
		{Role: "assistant", Text: "bbbb"},
		{Role: "user", Text: "cc"},
	}
	// Budget 9: "cc" (2) + "bbbb" (4) = 6 fit, "aaaa" (4) exceeds budget.
	// Without head alignment, "bbbb" (assistant) would be at index 0.
	// With head alignment, orphan assistant "bbbb" is dropped, leaving ["cc"] with role "user".
	w := Project(history, 9)
	if len(w.Messages) != 1 || w.Messages[0].Role != "user" || w.Messages[0].Text != "cc" {
		t.Fatalf("Project = %+v, want only user message [cc]", w.Messages)
	}
	if w.Dropped != 2 {
		t.Fatalf("Dropped = %d, want 2", w.Dropped)
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
	// Budget 4 is smaller than TruncationMarker (14 runes), so it is defensively clamped to 4 runes.
	w := Project(history, 4)
	if got := texts(w.Messages); !reflect.DeepEqual(got, []string{"very"}) {
		t.Fatalf("Messages = %v, want [very]", got)
	}
	if w.Dropped != 1 {
		t.Fatalf("Dropped = %d, want 1", w.Dropped)
	}
}

func TestProjectDefensivelyTruncatesWithMarker(t *testing.T) {
	history := []Message{{Text: "old"}, {Text: "a very long newest message that overflows"}}
	// Budget 20: marker is 14 runes, retains 6 runes prefix + "...[truncated]" = 20 runes.
	w := Project(history, 20)
	want := "a very" + TruncationMarker
	if got := texts(w.Messages); !reflect.DeepEqual(got, []string{want}) {
		t.Fatalf("Messages = %v, want [%s]", got, want)
	}
	if len([]rune(w.Messages[0].Text)) != 20 {
		t.Fatalf("rune length = %d, want 20", len([]rune(w.Messages[0].Text)))
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

func TestApplyProject(t *testing.T) {
	history := []Message{{Text: "aaa"}, {Text: "bbb"}, {Text: "cc"}}
	res, err := Apply(5, Op{Kind: "project", Messages: history})
	if err != nil {
		t.Fatalf("Apply(project): %v", err)
	}
	if got := texts(res.Messages); !reflect.DeepEqual(got, []string{"bbb", "cc"}) {
		t.Fatalf("project = %v, want [bbb cc]", got)
	}
	if res.Dropped != 1 {
		t.Fatalf("Dropped = %d, want 1", res.Dropped)
	}
	if _, err := Apply(5, Op{Kind: "echo"}); err == nil {
		t.Fatal("Apply(unknown) = nil, want error")
	}
}

func TestEstimateTokensBPE(t *testing.T) {
	// Standard English: "hello" (2) + "world" (2) = 4 tokens
	if got := EstimateTokens("hello world"); got != 4 {
		t.Errorf("EstimateTokens('hello world') = %d, want 4", got)
	}
	// Non-ASCII CJK expands to multi-token byte sequence
	cjk := "你好世界"
	if got := EstimateTokens(cjk); got < len([]rune(cjk))*2 {
		t.Errorf("EstimateTokens(%q) = %d, want >= 8", cjk, got)
	}
	// Punctuation and symbols split tokens
	code := "func main() { fmt.Println(123) }"
	if got := EstimateTokens(code); got < 10 {
		t.Errorf("EstimateTokens(code) = %d, want >= 10", got)
	}
}

func TestProjectModeBPE(t *testing.T) {
	history := []Message{
		{Role: "user", Text: "first question with some words"},
		{Role: "assistant", Text: "an answer that is detailed and explanatory"},
		{Role: "user", Text: "final brief follow up"},
	}
	// Total tokens: ~8 + ~8 + ~4 = ~20 tokens.
	// With budget 10 in BPE mode, only the newest message should fit.
	w := ProjectMode(history, 10, ModeBPE)
	if len(w.Messages) != 1 || w.Messages[0].Text != "final brief follow up" {
		t.Fatalf("ProjectMode(ModeBPE) = %+v, want only final follow up", w.Messages)
	}
	if w.Dropped != 2 {
		t.Fatalf("Dropped = %d, want 2", w.Dropped)
	}
}

func TestProjectModeBPEHeadroomForMultilingual(t *testing.T) {
	history := []Message{
		{Role: "user", Text: "Hello"},
		{Role: "user", Text: "こんにちは世界！这是一个测试。"},
	}
	// In rune mode, the CJK text is only 15 runes.
	// But in BPE mode, each CJK char counts as 2 tokens (~30 tokens).
	// With budget 20, in rune mode it would fit (15 < 20).
	// In BPE mode, it must recognize the expansion and truncate or drop older messages.
	w := ProjectMode(history, 20, ModeBPE)
	if len(w.Messages) != 1 {
		t.Fatalf("expected 1 message kept under BPE budget, got %d", len(w.Messages))
	}
	cost := TokenCost(w.Messages[0].Text, ModeBPE)
	if cost > 20 {
		t.Fatalf("projected message cost %d exceeds budget 20", cost)
	}
}
