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

The first cut fixes the session protocol and the basic commands; each chat
turn runs the composed pipeline over `memento.invoke`, and the model's
response arrives from the provider through model-manager.

## Protocol

- Session start: `repl: <nick> joined (type :help, :quit to leave)`.
- The prompt `you> ` precedes every read.
- `:help` returns one line naming the commands `:help` and `:quit`.
- `:quit`, `:q`, and `:exit` end the session.
- A blank line produces no response and the next prompt.
- Any other line is one chat turn: the guest runs the composed pipeline and
  prints exactly one response line (the model-manager response, which performs
  the provider call).
- End of input ends the session cleanly.

## Semantics

- `Session.Handle` is pure: it returns `Reply{Text, Quit}` for commands and
  delegates each chat turn to a responder `func(line string) (string, error)`;
  `Session.Run` hosts the loop over any reader (join line, prompts, responses,
  stop on quit or end of input) and reports responder errors in the transcript.
  The guest supplies WASI stdin, the kernel log as `emit`, and the pipeline
  responder. Commands and blank lines do not increment the turn counter.
- A chat turn runs over `memento.invoke`: append the user turn to
  `chat-history`, read recent turns, project them through `llm-context`, ask
  `model-registry` to respond (it resolves the view to its first concrete
  model), and append the assistant reply.
- The session never loads or unloads plugins: composition belongs to the
  loader (charter open question 3; system D4).
- The guest registers one effect on activation; its inverse emits
  `repl: session closed` on unload.

## ABI

- Exports: `memento_declare`, `memento_activate`, `memento_revert_effect`,
  `memory`.
- Imports: `memento.declare_inject`, `memento.declare_provide`,
  `memento.bind`, `memento.invoke`, `memento.get_payload_len`,
  `memento.get_payload`, `memento.register_effect`, `memento.log`.
- Declares: injects `chat-history`, `model-registry`, `llm-context`; provides
  `repl`.
- Binds: `repl` ← the session nickname (a tracked, revertible registration).
- Payload: the nickname string; the guest defaults to `agent` when empty.
- Stdio: WASI stdin for lines; the log import for output.

## Conformance

Colocated Go tests cover the protocol (commands, aliases, blank lines, turn
counting, responder delegation and errors). The `cmd/core-agent` end-to-end
test scripts a session through the composed system. Scenarios:
[`features/repl-chat.feature`](features/repl-chat.feature).

## Non-Goals

Loading or unloading plugins from the REPL; performing transport itself (the
exchange belongs to model-manager); persistence; multiple sessions; a
privileged LLM channel.

## Decisions

- **RC1 — Pipeline first.** A chat turn answers through the composed system
  (history → context → model over `invoke`), and the model step performs the
  real provider call.
- **RC2 — No plugin control in the REPL.** The REPL exposes session commands
  only; the loader composes (charter open question 3; system D4).
- **RC3 — Pure session logic.** The protocol lives in the host-testable
  library; the guest wires stdin/log, the kernel ABI, and the pipeline
  responder.
