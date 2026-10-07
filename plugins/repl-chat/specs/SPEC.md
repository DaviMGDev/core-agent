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

repl-chat is the terminal: the LLM and a human drive the same session. The
guest provides the key `repl` and injects `agent-loop`, so the kernel
activates it once the agent is available; the host entry drives the session
loop over this package's library.

The session protocol and the commands are fixed here; each chat turn is
handed to the agent over `memento.invoke`, and the agent's message is what
the terminal renders — from the `chat.message` the agent publishes, so a
prompted reply and an unprompted message take the same path. The pipeline
(history → context → model) lives in the agent plugin.

Reading stdin and being woken cannot both live in a serialized guest: while
the guest sits in an activation call it holds the module lock, so a host
wake cannot reach it. The host therefore owns the session loop (stdin, the
prompt, and the commands) and calls the guest's handler for one wake at a
time.

## Protocol

- Session start: `repl: <nick> joined (type :help, :quit to leave)`.
- The prompt `you> ` precedes every read.
- `:help` returns one line naming the commands `:help` and `:quit`.
- `:quit`, `:q`, and `:exit` end the session.
- A blank line produces no response and the next prompt.
- Any other line is one chat turn: the driver hands the line to the agent
  and exactly one response line appears when the agent speaks; a turn that
  starts a job or stays silent prints nothing.
- A `chat.message` event that arrives while the terminal waits for input is
  rendered the same way, with no prompt in between.
- End of input ends the session cleanly.

## Semantics

- `Session.Handle` is pure: it returns `Reply{Text, Quit}` for commands and
  delegates each chat turn to a responder `func(line string) (string, error)`;
  `Session.Run` hosts the loop over any reader (join line, prompts, responses,
  stop on quit or end of input) and reports responder errors in the transcript.
  The host driver runs it, wiring stdin, the terminal output as `emit`, and a
  responder that wakes the guest. Commands and blank lines do not increment
  the turn counter.
- A wake is `{line}` or `{events}` (the wire types in this package). A line
  runs one agent turn over `memento.invoke` into `agent-loop`; the message it
  speaks is published as `chat.message`, and the answer carries no text. A
  wake's `chat.message` events render one transcript line each (`Render`);
  a payload that carries a `text` renders it, any other payload renders
  verbatim. Failures ride the answer so the session reports them and
  continues.
- The driver waits for the wake it caused to drain (`Subscription.WaitIdle`)
  before the next prompt, so the reply renders in its turn; an unprompted
  message renders whenever its wake arrives.
- The transcript stays chat-history's; the terminal is a view. The session
  never loads or unloads plugins: composition belongs to the loader (charter
  open question 3; system D4).
- The guest registers one effect on activation; its inverse emits
  `repl: session closed` on unload.

## ABI

- Exports: `memento_declare`, `memento_activate`, `memento_revert_effect`,
  `memento_alloc`, `memento_handle`, `memory`.
- Imports: `memento.declare_inject`, `memento.declare_provide`,
  `memento.bind`, `memento.invoke`, `memento.get_payload_len`,
  `memento.get_payload`, `memento.register_effect`, `memento.log`.
- Declares: injects `agent-loop`; provides `repl`.
- Binds: `repl` ← the session nickname (a tracked, revertible registration).
- Payload: the nickname string; the guest defaults to `agent` when empty.
- Handler: `{line}` runs one agent turn; `{events}` renders the batch. The
  answer is `{}` or `{"error": ...}`. Output goes through the log import;
  the host driver owns stdin, the prompt, and the transcript sink.

## Conformance

Colocated Go tests cover the protocol (commands, aliases, blank lines, turn
counting, responder delegation and errors) and the render vocabulary
(`Render`, the wake round trip). The system scenarios cover the session
against the composed plugins, including an unprompted `chat.message`; the
`cmd/core-agent` end-to-end test scripts a session through the composed
system over the wasm ABI. Scenarios:
[`features/repl-chat.feature`](features/repl-chat.feature) and
[`../../../specs/features/repl.feature`](../../../specs/features/repl.feature).

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
  library; the guest wires the kernel ABI and the wake handler.
- **RC4 — The host drives the loop; the guest is woken.** Reading stdin and
  receiving a bus wake cannot both live in a serialized guest (the activation
  call holds the module lock), so the host runs `Session.Run` and calls the
  guest's handler once per wake. The driver hands the guest only chat lines;
  commands, blank lines, and the prompt never leave the driver.
