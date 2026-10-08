---
type: spec
title: "agent — Plugin Specification"
description: "The turn loop split out of repl-chat: wake to quiescence, at most one chat.message, jobs as tools, own context, agent-owned presentation, provider-native tool calls."
tags: [spec, plugin]
sections: [context, semantics, presentation, abi, conformance, non-goals, decisions]
created: "2026-10-06"
updated: "2026-10-06"
---

# agent — Plugin Specification

## Context

agent owns the LLM turn loop, split out of repl-chat (system D14, issue #3):
repl-chat keeps the terminal and invokes the agent; the host waker reaches the
same loop through the component's handler. One implementation serves the
top-level agent and every subagent. It provides the key `agent-loop` and
injects `chat-history`, `model-registry`, and `llm-context`.

## Semantics

- A turn runs from wake to quiescence: append the line, project the agent's
  own context, ask the model with the presented tool surface, act.
- The model may answer, call a tool, or say nothing. Silence is a legitimate
  outcome and speech is the model's call, not the host's.
- A turn yields at most one `chat.message`. A tool call starts a job and ends
  the turn without a message; the job's completion or a tick wakes the next
  turn. A turn yields at most one job: a provider answer carrying several
  tool calls takes its first.
- A model answer whose text parses as a JSON directive (`tool`, `silence`)
  carries the call; any other text is speech. A provider-native `tool_calls`
  answer reaches the agent already normalized to that same directive
  (model-manager's contract), so the directive is the single call path —
  the agent never parses provider wire shapes itself.
- A renamed callable's call maps back to the registry name before the job
  starts: the model speaks in presented labels, the job runs the registry's
  tool.
- Every agent works on its own conversation id and budget, provided by
  context-manager; the turn's projection is bounded.

## Presentation

The agent chooses what the model sees this turn: configured tools only,
hidden ones pruned, renamed ones relabeled. It never invents a callable the
registry does not hold — the configured surface is the registry's contents as
the assembler wired them.

Every presented callable carries its argument schema: the surface is
`{name, description, parameters}` per tool, and the schema travels with the
label through hiding and renaming — presentation relabels, it never rewrites
a schema. The agent hands the presented surface to model-manager on every
turn, and sends nothing else: no hidden tools, no registry names behind a
rename, no tools the configuration does not list.

## ABI

- Exports: `memento_declare`, `memento_activate`, `memento_handle`,
  `memento_revert_effect`, `memory`.
- Imports: `memento.declare_inject`, `memento.declare_provide`,
  `memento.bind`, `memento.invoke`, `memento.get_payload_len`,
  `memento.get_payload`, `memento.register_effect`, `memento.log`,
  `memento.job_start`, `memento.job_result_len`, `memento.job_result`,
  `memento.publish`.
- Declares: provides `agent-loop` (bound to the conversation id); injects
  `chat-history`, `model-registry`, `llm-context`.
- Handler: `{"line": ...}` for a terminal wake or `{"events": [...]}` for a
  bus wake; answers `{"text": ...}` or `{"job": ...}`.
- The `respond` operation carries the presented surface as `tools` — a JSON
  wire shape of `{name, description, parameters}` per callable — so the
  provider request can offer the tools natively; the answer comes back as
  text, with a native `tool_calls` answer already normalized to the
  `{"tool", "args"}` directive.
- A wake may carry `{"config": {...}}` — the turn's configuration, used for
  that wake only: a subagent's own conversation, model, budget, and tool
  surface. It may carry `{"dump": true}`, and the answer then includes
  `{"conversation": [...]}`, the turns the wake ran on. `{"private": true}`
  marks a subagent turn: its speech stays in the child's conversation and is
  not published for a renderer.
- Payload: the agent's JSON configuration (conversation, model, budget,
  tools, hidden, renamed).
- Effect inverse on unload: `agent: loop closed`.

## Conformance

Colocated Go tests cover the turn semantics and presentation; the handler and
waker paths are exercised by the entry's end-to-end test. Scenarios:
[`features/agent.feature`](features/agent.feature).

## Non-Goals

Persistence; multi-agent orchestration beyond the subagent issue; tool
implementation; model transport.

## Decisions

- **A1 — A turn yields at most one message.** Speech is the model's call;
  silence is normal.
- **A2 — A tool call ends the turn.** The job's completion or a tick wakes
  the next turn; the agent never waits on a job.
- **A3 — The agent owns its context.** Its conversation id and budget are
  configuration; per-agent differences are data.
- **A4 — Presentation never invents.** The presented surface is a projection
  of configured tools: hide, rename, prune — nothing else.
- **A5 — Configuration travels in the wake.** The activation payload is the
  agent's default; a wake may override it for its turn, which is how one
  implementation serves the top-level agent and every subagent without
  subagent-specific code.
- **A6 — The directive is the single call path.** Provider-native function
  calling is normalized to the `{"tool", "args"}` directive before the
  answer reaches the agent; the text-directive path stays for mock and
  scripted providers, and the agent holds no provider-specific parse.
- **A7 — Presentation owns the rename, both ways.** The presented label is
  what the model sees and says; the agent reverse-maps an alias to the
  registry name before starting the job, so the registry never learns the
  label.
