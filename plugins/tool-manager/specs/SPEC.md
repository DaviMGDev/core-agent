---
type: spec
title: "tool-manager — Plugin Specification"
description: "The tool registry and job engine: every call a job, start/peep/kill, ticks, and job events."
tags: [spec, plugin]
sections: [context, semantics, jobs, events, conformance, non-goals, decisions]
created: "2026-10-06"
updated: "2026-10-06"
---

# tool-manager — Plugin Specification

## Context

tool-manager is the agent layer's hands, host-side per system D14: it owns
the registry of callable tools and the execution of every call as a job. It
implements the loader's host-services contract, so guests reach it through
the job imports (memento `plugins/wasm` spec D11-D13), and `cmd/core-agent`
injects it together with the notification bus.

## Semantics

- A tool is `{name, description, argument schema, runner}`. The owner
  declares; the manager lists. A missing name, a missing runner, or a
  duplicate name is refused and the registry stays unchanged.
- A call is a job, always: `Start(tool, args, tick?)` returns a handle at
  once and never waits for the call to finish.
- A job's life: queued → running → done | failed, or killed from anywhere.
- A refused start is a job too: it fails at once, with the reason.
- `Peep(job)` returns state, last tick, and output so far.
- `Kill(job)` is accepted at any point; the job is marked killed at once,
  its runner unwinds at the next safe point, and killing a job kills its
  descendants. A terminal job is left as it is.
- `Close` reclaims every job: runners are cancelled and their jobs land
  failed with "tool manager closed". Reclamation is not a kill.
- A job carries its caller: the instance the guest import ran on, when it
  came through `job_start`. `Depth` walks the tree (a root job is 1).
- A job started while the caller's instance is attributed to a parent job is
  adopted into that job's subtree, before its runner starts.

## Jobs

The tick interval is the caller's (`tick_ms`), else the manager's default
(30s, tunable at construction). A tick carries elapsed time: a clock nudge —
time passed, peep me — never a health claim; a missing tick means nothing.
Output is write-through: what the runner writes, peep returns mid-run.

## Events

A job event carries the job: `job.started` with the job and tool,
`job.tick` with elapsed, `job.completed` with the result, `job.failed` with
the error, and `job.killed`. A reclaimed job emits nothing.

Routing follows the tree: a job with a parent delivers its events to the
parent's listener (`Listen`/`Unlisten`) and never to the bus — the parent is
the sole listener to its child's subtree events — while a root job publishes
on the bus. An event whose parent has no listener is dropped.

## Conformance

Colocated Go tests cover the registry, start/peep/kill, ticks, and
reclamation; the guest path is covered by the loader's ABI tests and the
entry's end-to-end test. Scenarios:
[`features/tool-manager.feature`](features/tool-manager.feature).

## Non-Goals

Persistence of jobs; scheduling policy or priorities; quotas; retries;
cross-process tools.

## Decisions

- **TM1 — A call is a job, always.** There is no fast path: even `read` runs
  as a job, so lifecycle, observation, and kill are uniform.
- **TM2 — Refused starts are jobs.** The caller always gets a handle; an
  unknown tool lands in a failed job carrying the reason.
- **TM3 — Kill decides at once, unwinds at the safe point.** The job's state
  is killed immediately; the runner is cancelled and returns at its next safe
  point. Descendants are killed with their parent.
- **TM4 — Reclamation is not a kill.** Closing the manager cancels runners
  without kill semantics or events; a late runner return cannot resurrect the
  job.
- **TM5 — Ticks are nudges.** A tick reports elapsed time, never liveness.
- **TM6 — The tree is the routing table.** Adoption happens at start, before
  the runner runs, so a child's first event already routes to its parent; no
  filter over a global topic can leak a subtree to another agent.
- **TM7 — Attribution is per call.** `Attribute`/`Release` name the job an
  instance is executing for; the caller's cancellation poll answers for it,
  and jobs it starts are adopted under it.
