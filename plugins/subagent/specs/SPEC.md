---
type: spec
title: "subagent — Component Specification"
description: "An agent callable as a tool: the manager job whose runner drives one child agent loop, isolated in and bounded in depth."
tags: [spec, plugin]
sections: [context, semantics, call, visibility, conformance, non-goals, decisions]
created: "2026-10-06"
updated: "2026-10-06"
---

# subagent — Component Specification

## Context

A subagent is an agent the tool manager can call as a tool (charter, issue
#5). One implementation serves the top-level agent and every subagent: this
host-side component registers a manager tool whose runner drives the agent
loop for one child — the same `memento_handle` the terminal and the host
waker reach — with the child's own conversation, model, and budget.

The runner is host-side because a subagent call must be a job: the manager
owns the job, the runner is its body, and the parent job is the child's sole
listener. `cmd/core-agent` and the conformance fixture register the tool with
the manager.

## Semantics

- A subagent call is a job, always: `start` returns a handle at once, `peep`
  observes, `kill` is accepted at any point, and the job reaches
  done | failed like any other. No fast path.
- The child's conversation and context are its own. It receives the brief and
  nothing else of the parent's; the conversation id is derived from the job,
  so two calls never share a context. Its turns are private: the child's
  speech stays in its conversation and is not published for a renderer; only
  the parent's turn surfaces the result.
- The child's model is configuration, resolved by model-manager like any
  agent; a per-call `model` overrides the tool's default.
- The runner drives the child's turns: it invokes the agent loop with the
  brief, and when the turn ends in a job or in silence it waits for the
  child's subtree events (the parent is the sole listener) and wakes the
  next turn with them. The call completes when the child speaks.
- While the runner invokes the child's turn, the job is attributed to the
  caller's instance, so the child's host imports answer for it — a killed
  call unwinds the turn at its next safe point — and jobs the child starts
  are adopted as its children.
- The caller chooses the return shape per call: the final reply (default),
  or the whole child conversation. The reply is private otherwise.
- A subagent may call subagents. The job tree is bounded by configuration
  (default depth 2): a call whose job depth exceeds the bound fails like any
  failed job, with the reason.
- Visibility and kill are subtree-only: a job's events go to its parent's
  listener, never to the bus; killing a job kills its descendants.

## Call

The tool is `subagent`; its arguments are a JSON document:

| Field | Meaning |
|---|---|
| `brief` | the child's only input (required) |
| `return` | `reply` (default) or `conversation` |
| `model` | the child's model view; the tool default otherwise |
| `budget` | the child's context budget; the tool default otherwise |

The job's result is `{reply}` or `{reply, conversation}`.

The runner waits on its job's listener for the events of its direct
children, skipping `job.started` (a start is not a wake reason). Ticks,
completions, failures, and kills wake the child's next turn. A silent turn
waits too; the child's next turn decides again.

## Visibility

A job's events are delivered to the parent job's listener when it has one,
and published on the bus only for root jobs. The top-level agent hears its
own jobs; a subagent hears its children; no other agent sees a child's
events. The guest-facing `peep` and `kill` imports answer within the calling
job's subtree only.

## Conformance

Colocated Go tests cover the runner, the depth bound, and the return shapes
against a fake agent; the manager's job tree and routing are covered by the
tool-manager suite. Scenarios:
[`features/subagent.feature`](features/subagent.feature).

## Non-Goals

Persistence; scheduling or prioritization between subagents; shared memory
between parent and child beyond the call's brief and result; per-child
transport policy.

## Decisions

- **SA1 — The runner drives the child.** The call's runner invokes the loop,
  waits on the child's events, and completes when the child speaks. A
  subagent's turns live inside its job, so the call's result is the reply and
  the parent needs no separate wake path.
- **SA2 — The parent is the sole listener.** A job's events route to its
  parent's listener; only root jobs publish on the bus. Subtree visibility
  falls out of the tree, not out of filtering a global topic.
- **SA3 — Attribution is scoped to the guest call.** The runner attributes
  the job to the caller's instance around each agent invocation and releases
  it while waiting, so cancellation and child adoption answer for the turn in
  flight only.
- **SA4 — Depth is checked at the call.** The bound counts job depth from the
  root; past it, the call fails with the reason like any failed job. The
  default is 2.
- **SA5 — Configuration is data.** The child's conversation, model, budget,
  and tool surface travel in the wake's config; the agent loop has no
  subagent-specific code.
