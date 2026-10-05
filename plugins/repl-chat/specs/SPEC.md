---
type: spec
title: "repl-chat — Plugin Specification"
description: "Interactive REPL plugin: reads lines from WASI stdin, answers commands, returns one response per chat turn."
tags: [spec, plugin]
sections: [context, protocol, semantics, abi, conformance, non-goals, decisions]
created: "2026-10-05"
updated: "2026-10-05"
---

# repl-chat — Plugin Specification

## Context

repl-chat is the user-facing plugin: the LLM and a human drive the same session.
It is a wasm guest (WASI stdio) that provides the key `repl` and injects
`chat-history`, `model-registry`, and `llm-context`, so the kernel activates it
only once its dependencies are available.

The first cut fixes the session protocol and the basic commands; responses are
a deterministic local stub until the transport extension (system D6) lets the
model plugins answer.

## Protocol

- Session start: `repl: <nick> joined (type :help, :quit to leave)`.
- The prompt `you> ` precedes every read.
- `:help` returns one line naming the commands `:help` and `:quit`.
- `:quit`, `:q`, and `:exit` end the session.
- A blank line produces no response and the next prompt.
- Any other line produces exactly one response line; the first-cut stub is
  `turn <n>: <line>`, where `<n>` is the 1-based chat-turn counter.
- End of input ends the session cleanly.

## Semantics

- `Session.Handle` is pure: it returns `Reply{Text, Quit}`; `Session.Run`
  hosts the loop over any reader (join line, prompts, responses, stop on
  quit or end of input), and the guest supplies WASI stdin plus the kernel
  log as `emit`. Commands and blank lines do not increment the turn counter.
- The session never loads or unloads plugins: composition belongs to the
  loader (charter open question 3; system D4).
- The guest registers one effect on activation; its inverse emits
  `repl: session closed` on unload.

## ABI

- Exports: `memento_declare`, `memento_activate`, `memento_revert_effect`,
  `memory`.
- Imports: `memento.declare_inject`, `memento.declare_provide`,
  `memento.get_payload_len`, `memento.get_payload`, `memento.register_effect`,
  `memento.log`.
- Declares: injects `chat-history`, `model-registry`, `llm-context`; provides
  `repl`.
- Payload: the nickname string; the guest defaults to `agent` when empty.
- Stdio: WASI stdin for lines; the log import for output.

## Conformance

Colocated Go tests cover the protocol (commands, aliases, blank lines, turn
counting, stub shape). The `cmd/core-agent` end-to-end test scripts a session
through the composed system. Scenarios:
[`features/repl-chat.feature`](features/repl-chat.feature).

## Non-Goals

Loading or unloading plugins from the REPL; provider transport; persistence;
multiple sessions; a privileged LLM channel.

## Decisions

- **RC1 — Stub first.** The first cut answers with a deterministic local stub;
  transport-backed answers arrive with the ABI extension, not through a hidden
  host path.
- **RC2 — No plugin control in the REPL.** The REPL exposes session commands
  only; the loader composes (charter open question 3; system D4).
- **RC3 — Pure session logic.** The protocol lives in the host-testable
  library; the guest only wires stdin/log and the kernel ABI.
