---
type: spec
title: "bash — Component Specification"
description: "A default shell tool: one call is one job, merged streaming to peep, a factual result, process-tree kill, and a 60s timeout bound."
tags: [spec, plugin]
sections: [context, semantics, call, conformance, non-goals, decisions]
created: "2026-10-10"
updated: "2026-10-10"
---

# bash — Component Specification

## Context

`bash` is the default shell tool: the agent's way to touch the filesystem, run
a build, or read a file with the shell (charter, issue #22). It is a
host-side component beside the guest plugins (system D14) and is declared by
`cmd/core-agent` into the tool manager's registry beside the subagent and the
job surface, so the top-level agent presents it natively and subagents inherit
it through the registry surface they already receive.

The tool is deliberately a stress test for the job model: a call is
long-running and streaming, a kill must reach a real process, and a failure
must come back factually (the issue #18 policy). Nothing here is a new
capability the model can claim beyond what the tool returns.

## Semantics

- One call is one job, always: `start` returns a handle at once, `peep`
  observes, `kill` is accepted at any point, and the job reaches
  done | failed | killed like any other. No fast path.
- The command runs through the shell in the core's environment: `bash -c`
  when bash is on PATH, `sh -c` otherwise; when neither exists the call fails
  factually. The core's working directory and environment are inherited;
  `cwd` overrides the directory for that call.
- The call runs in its own process group. A kill cancels the runner's context,
  the runner kills the process group, and the job lands killed, not done —
  even though the runner returns after the cancellation.
- stdout and stderr stream to the job's write-through buffer in arrival
  order, so peep shows them while the command runs, not only at completion.
- A finished call's result is factual: `{exit_code, stdout, stderr}`. The
  streams stay split in the result even though peep sees them merged.
- A non-zero exit fails the job; the error text is the observed state plus
  the captured output, never an invented diagnosis. The captured output also
  stays observable through peep's output buffer.
- An optional `timeout_ms` bounds the call; the default is 60 seconds. Expiry
  terminates the process group and fails the job with the timeout it
  observed.
- The tool is presented like any other callable: hiding and renaming apply,
  and the description binds the model to report only what the process
  produced.

## Call

The tool is `bash`; its arguments are a JSON document:

| Field | Meaning |
|---|---|
| `command` | the shell command to run (required) |
| `cwd` | the working directory; the core's directory when unset |
| `timeout_ms` | the time bound in milliseconds; 60000 when unset |

The job's result on done is `{exit_code, stdout, stderr}`.

## Conformance

Colocated Go tests cover the declaration, shell selection, environment
inheritance, streaming, the result document, the failure policy, process-tree
kill, and the timeout bound. Scenarios:
[`features/bash.feature`](features/bash.feature), enrolled in the conformance
suite.

## Non-Goals

Runaway-job budgets and quotas (issue #20); long-result presentation in the
transcript (issue #21); PTY or interactive shell sessions; shells other than
bash/sh; Windows-specific process handling — kill targets the POSIX process
group.

## Decisions

- **B1 — Shell selection is bash, then sh.** `bash -c` is the primary path;
  `sh -c` is the fallback when bash is absent (user decision), so the tool
  works on systems where bash is not installed. Neither present fails the
  call factually.
- **B2 — Failure is factual, not diagnostic.** A non-zero exit fails the job;
  the error text carries the exit code and the captured output verbatim. No
  job-engine change is needed: the engine records the result on success and
  the error text on failure, and the failure facts reach the model through
  the error the wake carries and through peep's output buffer.
- **B3 — Peep merged, result split.** Both streams write to the job's buffer
  as they arrive (arrival order), while the result keeps stdout and stderr
  separate, so observation is live and the result is parseable.
- **B4 — Kill the process group.** The call sets `Setpgid`, and cancellation
  sends SIGKILL to the group, so the command's descendants die with the shell.
  `WaitDelay` bounds the wait for I/O to close after a kill.
- **B5 — The timeout defaults to 60 seconds.** `timeout_ms` overrides it per
  call (user decision); expiry is the call's own bound, not a job-engine
  policy.
