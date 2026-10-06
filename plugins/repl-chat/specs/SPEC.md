---
type: spec
title: "repl-chat — Plugin Specification"
description: "Interactive REPL plugin: reads lines from WASI stdin, answers commands, returns one response per chat turn."
tags: [spec, plugin]
sections: [context, protocol, semantics, abi, conformance, non-goals, decisions]
created: "2026-10-05"
updated: "2026-10-06"
---

# repl-chat — Plugin Specification

## Context

repl-chat is the terminal: the LLM and a human drive the same session. It is
a wasm guest (WASI stdio) that provides the key `repl` and injects
`agent-loop`, so the kernel activates it once the agent is available.

The session protocol and the commands are fixed here; each chat turn is
handed to the agent over `memento.invoke`, and the agent's message is what
the terminal renders. The pipeline (history → context → model) lives in the
agent plugin.

## Protocol

- Session start: `repl: <nick> joined (type :help, :quit to leave)`.
- The prompt `you> ` precedes every read.
- `:help` returns one line naming the commands `:help` and `:quit`.
- `:quit`, `:q`, and `:exit` end the session.
- A blank line produces no response and the next prompt.
- Any other line is one chat turn: the guest hands the line to the agent and
  prints exactly one response line when the agent speaks; a turn that starts
  a job or stays silent prints nothing.
- End of input ends the session cleanly.

## Semantics

- `Session.Handle` is pure: it returns `Reply{Text, Quit}` for commands and
  delegates each chat turn to a responder `func(line string) (string, error)`;
  `Session.Run` hosts the loop over any reader (join line, prompts, responses,
  stop on quit or end of input) and reports responder errors in the transcript.
  The guest supplies WASI stdin, the kernel log as `emit`, and the pipeline
  responder. Commands and blank lines do not increment the turn counter.
- A chat turn runs over `memento.invoke` into `agent-loop`: the agent runs
  its turn (history, context, model, jobs) and answers with the message it
  spoke, if any. The transcript stays chat-history's; the terminal is a view.
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
- Declares: injects `agent-loop`; provides `repl`.
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

- **RC1 — The agent owns the pipeline.** A chat turn is delegated to
  `agent-loop`; the terminal keeps stdin, the prompt, the commands, and the
  rendering. The pipeline moved to the agent plugin (issue #3).
- **RC2 — No plugin control in the REPL.** The REPL exposes session commands
  only; the loader composes (charter open question 3; system D4).
- **RC3 — Pure session logic.** The protocol lives in the host-testable
  library; the guest wires stdin/log, the kernel ABI, and the pipeline
  responder.
