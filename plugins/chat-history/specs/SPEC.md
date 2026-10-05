---
type: spec
title: "chat-history — Plugin Specification"
description: "Keeps the conversation record: append, list, and serve recent turns for ongoing discussions."
tags: [spec, plugin]
sections: [context, semantics, abi, conformance, non-goals, decisions]
created: "2026-10-05"
updated: "2026-10-05"
---

# chat-history — Plugin Specification

## Context

chat-history keeps the conversation record. It provides the key
`chat-history` and injects nothing; context-manager projects its turns and
repl-chat names it as a dependency.

The first cut is an in-memory store: appending, listing, and serving recent
turns. Persistence is a non-goal until a spec asks for it.

## Semantics

- A message is `{role, text}`; `role` is `user` or `assistant`; anything else
  is refused.
- A conversation is identified by a non-empty id. `Append` starts the
  conversation if absent, so recording never blocks on setup.
- `Messages` returns the conversation's turns in append order.
- `Recent(id, n)` returns the last `n` turns in append order; `n <= 0`
  returns nothing, `n` beyond the length returns everything.
- `Conversations` returns ids in creation order; the store is safe for
  concurrent use by the kernel's workers.

## ABI

- Exports: `memento_declare`, `memento_activate`, `memento_revert_effect`,
  `memory`.
- Imports: `memento.declare_inject`, `memento.declare_provide`,
  `memento.get_payload_len`, `memento.get_payload`, `memento.register_effect`,
  `memento.log`.
- Declares: provides `chat-history`; injects nothing.
- Payload: conversation id (string); the guest defaults to `default`.
- Effect inverse on unload: `chat-history: conversation closed`.

## Conformance

Colocated Go tests cover append ordering, role validation, recent windows,
conversation listing, and restarting a conversation id. The guest path is
exercised by the `cmd/core-agent` end-to-end test. Scenarios:
[`features/chat-history.feature`](features/chat-history.feature).

## Non-Goals

Persistence; editing or deleting messages; search; token accounting;
multi-user access control.

## Decisions

- **CH1 — In-memory first.** The record lives in the plugin library; a store
  backend arrives only with a spec that needs it.
- **CH2 — Roles are fixed.** `user` and `assistant` are the whole language of
  the record; extensions require a spec change.
- **CH3 — Recent windows are ordered.** `Recent` serves turns in append
  order, newest at the end, matching how a context window reads.
