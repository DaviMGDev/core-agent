---
type: spec
title: "context-manager — Plugin Specification"
description: "Manages the LLM context: starts as a projection of chat history into a bounded window."
tags: [spec, plugin]
sections: [context, semantics, projection, abi, conformance, non-goals, decisions]
created: "2026-10-05"
updated: "2026-10-05"
---

# context-manager — Plugin Specification

## Context

context-manager manages the LLM context. It starts as a projection of chat
history: it takes recorded turns and produces the bounded window a model call
would receive. It provides the key `llm-context` and injects `chat-history`,
so the kernel activates it after history is available.

## Semantics

- A message is `{role, text}` (the same shape chat-history records).
- Budget is counted in runes of message text.
- `Project(history, budget)` returns `{messages, dropped, budget}`:
  - newest messages are kept first; older ones are dropped while the running
    text total exceeds the budget;
  - at least the newest message is always kept, even when it alone exceeds the
    budget;
  - `dropped` counts omitted messages; kept messages keep append order.
- An empty history projects an empty window with no dropped messages.
- `budget <= 0` keeps only the newest message.

## Projection

The projection is a pure function: same history and budget, same window. It
never talks to chat-history directly — the assembler feeds turns in, which is
what keeps the plugin isolated and the projection testable on the host. Once
cross-guest data flow lands (system D8), the guest will feed the projection
from the `chat-history` binding.

## ABI

- Exports: `memento_declare`, `memento_activate`, `memento_revert_effect`,
  `memory`.
- Imports: `memento.declare_inject`, `memento.declare_provide`,
  `memento.get_payload_len`, `memento.get_payload`, `memento.register_effect`,
  `memento.log`.
- Declares: provides `llm-context`; injects `chat-history`.
- Payload: JSON `{"budget":<runes>}`; the guest defaults to `4096`.
- Effect inverse on unload: `context-manager: window released`.

## Conformance

Colocated Go tests cover exact-fit windows, truncation, the keep-newest
guarantee, empty history, and non-positive budgets. The guest path is
exercised by the `cmd/core-agent` end-to-end test. Scenarios:
[`features/context-manager.feature`](features/context-manager.feature).

## Non-Goals

Summarization; tokenizers or model-specific counting; retrieving from
chat-history directly; persistence; prompt templates.

## Decisions

- **CM1 — Pure projection.** The window is a function of turns and budget;
  the guest owns the ABI, not the arithmetic.
- **CM2 — Keep the newest.** A model call must see at least the latest turn;
  the budget never empties the window.
- **CM3 — Runes now.** A real tokenizer arrives only with a spec that needs
  it; runes are deterministic and sufficient for the first cut.
