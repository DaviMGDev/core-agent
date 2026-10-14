// Package contextmanager projects a recorded conversation into the bounded
// window an LLM call would receive. The projection is a pure function of
// turns and budget; the guest owns the ABI.
package contextmanager

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// DefaultBudget is the guest's budget when the payload leaves it unset.
const DefaultBudget = 4096

// ModeRune counts budget by Unicode runes (legacy mode).
const ModeRune = "rune"

// ModeBPE estimates budget using tokenizer-aware BPE subword modeling (default).
const ModeBPE = "bpe"

// TruncationMarker is appended when an oversized newest message is truncated to fit the budget.
const TruncationMarker = "...[truncated]"

// EstimateTokens calculates a tokenizer-aware BPE token estimation for text.
// Subword tokenizers (tiktoken cl100k, Llama/Gemma BPE) average ~4 characters per token
// for English words, count punctuation and symbols separately, attach whitespace
// to words, and expand non-ASCII (CJK, Arabic, Cyrillic, Emoji) runes into 1-3 byte-level tokens.
func EstimateTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	tokens := 0
	wordRun := 0

	for _, r := range s {
		if r <= 127 {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				wordRun++
			} else if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
				if wordRun > 0 {
					tokens += (wordRun + 3) / 4
					wordRun = 0
				}
			} else {
				if wordRun > 0 {
					tokens += (wordRun + 3) / 4
					wordRun = 0
				}
				tokens++ // punctuation and symbols
			}
		} else {
			if wordRun > 0 {
				tokens += (wordRun + 3) / 4
				wordRun = 0
			}
			// Multi-byte runes expand in byte-level BPE:
			// CJK characters average ~1.5 - 2.5 tokens per character
			if (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3040 && r <= 0x30FF) {
				tokens += 2
			} else if r > 0x1F000 {
				tokens += 2 // Emojis / extended symbols
			} else {
				tokens++ // Accented latin, Cyrillic, Greek, etc.
			}
		}
	}
	if wordRun > 0 {
		tokens += (wordRun + 3) / 4
	}
	if tokens == 0 && len(s) > 0 {
		tokens = 1
	}
	return tokens
}

// TokenCost calculates the token cost of text according to the given mode.
// Defaults to ModeBPE if mode is empty or unrecognized.
func TokenCost(text string, mode string) int {
	if mode == ModeRune {
		return len([]rune(text))
	}
	return EstimateTokens(text)
}

// Message is one turn in the window.
type Message struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// Window is a budgeted projection of a conversation.
type Window struct {
	// Messages are the kept turns in append order.
	Messages []Message
	// Dropped counts the omitted turns.
	Dropped int
	// Budget is the token/rune budget the window was projected against.
	Budget int
	// Mode is the counting mode ("bpe" or "rune").
	Mode string
}

// Project keeps the newest messages that fit budget — counted using tokenizer-aware
// BPE estimation (or rune counting if configured) — and always keeps the newest
// message, even when it alone exceeds the budget. Kept messages preserve append order.
// When older messages are pruned to fit budget, any leading assistant turn is dropped
// so index 0 maintains user-first head alignment.
func Project(history []Message, budget int) Window {
	return ProjectMode(history, budget, ModeRune)
}

// ProjectMode projects the history using the specified counting mode (ModeBPE or ModeRune).
func ProjectMode(history []Message, budget int, mode string) Window {
	if mode == "" {
		mode = ModeBPE
	}
	w := Window{Budget: budget, Mode: mode}
	if len(history) == 0 {
		return w
	}
	var kept []Message
	total := 0
	for i := len(history) - 1; i >= 0; i-- {
		m := history[i]
		cost := TokenCost(m.Text, mode)
		if len(kept) == 0 {
			if budget > 0 && cost > budget {
				runes := []rune(m.Text)
				markerCost := TokenCost(TruncationMarker, mode)
				if budget > markerCost {
					// Defensively truncate prefix until prefix + marker fits budget
					maxPrefix := budget - markerCost
					if maxPrefix > len(runes) {
						maxPrefix = len(runes)
					}
					for maxPrefix > 0 && TokenCost(string(runes[:maxPrefix])+TruncationMarker, mode) > budget {
						maxPrefix--
					}
					m.Text = string(runes[:maxPrefix]) + TruncationMarker
				} else {
					maxRunes := budget
					if maxRunes > len(runes) {
						maxRunes = len(runes)
					}
					for maxRunes > 0 && TokenCost(string(runes[:maxRunes]), mode) > budget {
						maxRunes--
					}
					m.Text = string(runes[:maxRunes])
				}
				cost = TokenCost(m.Text, mode)
			}
			kept = append(kept, m)
			total += cost
			continue
		}
		if budget <= 0 || total+cost > budget {
			break
		}
		kept = append(kept, m)
		total += cost
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	if len(kept) < len(history) {
		for len(kept) > 0 && kept[0].Role == "assistant" {
			kept = kept[1:]
		}
	}
	w.Messages = kept
	w.Dropped = len(history) - len(kept)
	return w
}

// Config is the guest's activation payload: {"budget":<runes>, "mode":<"bpe"|"rune">}.
type Config struct {
	Budget int    `json:"budget"`
	Mode   string `json:"mode,omitempty"`
}

// ParseConfig reads a {"budget":N} payload. An empty payload is the zero
// config; the guest applies DefaultBudget for a zero budget.
func ParseConfig(payload []byte) (Config, error) {
	var c Config
	if len(bytes.TrimSpace(payload)) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return Config{}, fmt.Errorf("context-manager: parsing config: %w", err)
	}
	return c, nil
}

// Op is one operation requested over the context window.
type Op struct {
	Kind     string    `json:"op"`
	Messages []Message `json:"messages,omitempty"`
	Mode     string    `json:"mode,omitempty"`
}

// Result is the response of an operation.
type Result struct {
	Messages []Message `json:"messages,omitempty"`
	Dropped  int       `json:"dropped,omitempty"`
	Mode     string    `json:"mode,omitempty"`
}

// Apply runs one operation against the configured budget: "project" builds
// the bounded window over the supplied turns.
func Apply(budget int, op Op) (Result, error) {
	switch op.Kind {
	case "project":
		mode := op.Mode
		if mode == "" {
			mode = ModeRune
		}
		w := ProjectMode(op.Messages, budget, mode)
		return Result{Messages: w.Messages, Dropped: w.Dropped, Mode: w.Mode}, nil
	default:
		return Result{}, fmt.Errorf("context-manager: unknown operation %q", op.Kind)
	}
}
