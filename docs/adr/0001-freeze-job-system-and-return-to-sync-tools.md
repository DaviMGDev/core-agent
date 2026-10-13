# ADR 0001: Freeze Background Job System and Transition to Synchronous Blocking Tool Calling

- **Status:** Accepted
- **Date:** 2026-10-13
- **Deciders:** Core Agent Team

---

## Context

Early in `core-agent`'s development, an asynchronous background job subsystem and event-driven notification queue were introduced (issues #1–#7, #15–#17, #20, #27, #29, #46, #48). In this model, tool invocations could spawn asynchronous background jobs that published intermediate "ticks" and completion events over a Link/Memento notification bus. The agent loop relied on background wake events to inspect job outputs and resume conversation turns.

While ambitious and creative, this subsystem introduced compounding runtime complexity and stability issues:
1. **Tick Re-execution Spam:** Intermediate job ticks frequently prompted models to hallucinate or re-issue duplicate tool executions while a job was still executing (#20, #29).
2. **Interactive Turn Dropping:** When background jobs ran concurrently with interactive human chat turns, race conditions caused incoming user turns to be dropped or blocked (#46).
3. **Turn Silence Failures:** Background wakes on terminal job events often resulted in empty model turns or agent silence, failing to deliver completed results back to the user (#48).
4. **Cognitive & Semantic Split:** The agent turn loop had to juggle dual semantics between interactive prompt wakes and autonomous background notification wakes (#44).

Meanwhile, foundational capabilities required by modern autonomous agents remain pending:
- Tokenizer-aware BPE budgeting (#34)
- Full execution graph persistence and multi-turn tool recall (#33)
- Dynamic context assembly accounting for tool schemas and system prompt footprints (#37)
- Streaming intermediate status/thoughts during tool execution (#40)
- Durable checkpointing for conversation history (#41)
- Rich provider telemetry structures (reasoning tokens, usage, cache hits) (#42)

---

## Decision

We decide to:
1. **Freeze the background job subsystem:**
   - Pause further feature development on asynchronous background jobs, periodic tick loops, and job notification wakes.
   - Mark job management tools (`jobs_list`, `jobs_peek`, `jobs_kill`) as deprecated/dormant.
   - Deactivate background tick wake cycles in the main runtime loop.
2. **Transition to synchronous, blocking standard tool calling:**
   - Tools are invoked synchronously and block within `agent.RunTurn`.
   - Tool outputs are immediately correlated and returned to the model within the current execution turn.
   - Multi-step tool runs follow the standard sequential or parallel tool calling cycle used by mainstream agent runtimes.
3. **Preserve architecture for future reintroduction:**
   - Do **not** destroy the architectural specifications or history of the job subsystem.
   - Explicitly document the prerequisites for reviving background jobs in the future once core runtime foundations mature.

---

## Future Roadmap: Returning to the Job System

This freeze is **temporary and strategic**, not a permanent abandonment. Asynchronous background jobs and long-running workers provide clear value for tasks that exceed human interactive patience (e.g. large builds, background scraping, or daemon tasks).

However, the job subsystem cannot succeed if the foundational substrate underneath it is unstable. We will reintroduce the background job system after the following milestones are achieved:

1. **Rock-Solid Core Memory & Graph:** Full multi-turn execution graph persistence (#33) and durable session checkpointing (#41).
2. **Accurate Context Budgeting:** Tokenizer-aware BPE budgeting (#34) and dynamic system/tool footprint deduction (#37).
3. **Rich Telemetry & Streaming:** Preserved provider metadata and streaming intermediate progress/reasoning (#40, #42).
4. **Formal Isolation State Machine:** A clean concurrency model separating interactive conversational state from detached worker state, avoiding shared turn lockouts.

---

## Affected Issues & Status

| Issue | Title | Action under Freeze |
|---|---|---|
| **#1** | Tool manager: a job for every call | Archived context (historical foundation) |
| **#2** | Notifications: a queued event bus with job/chat topics | Maintained for core events; job topics dormant |
| **#7** | [Epic] The agent layer: jobs, notifications, agents | Preserved as historical epic |
| **#15** | TUI: a running-jobs panel with peek output | UI panel hidden/dormant during freeze |
| **#16** | Agent: ticks must never paint the screen | Obsolete during freeze; tick wakes inactive |
| **#17** | Agent tools: list, peep, and kill jobs | Tools deprecated/disabled |
| **#20** | Investigate: minutes-long ticking jobs runaway | Preserved as case study for future job design |
| **#27** | Architecture: decouple tools from jobs | Confirmed: sync tool calling prioritized |
| **#29** | Agent: intermediate job ticks duplicate spam | Preserved as case study for future job design |
| **#40** | Turn Loop: Streaming intermediate thoughts | Refocused onto inline tool call streaming |
| **#44** | Discussion: Turn termination on silence (user vs wake) | Deferred until job system reintroduction; labeled `deferred:job-system` |
| **#46** | Agent: Interactive user turns silently dropped | Addressed; verified no background interference |
| **#48** | fix(agent): agent stays silent on job completion | Addressed; terminal job wakes resolved |

---

## Consequences

### Positive
- **Simplicity:** The turn loop becomes deterministic, easier to reason about, and easier to debug.
- **Reliability:** Eliminates race conditions between user turns and background ticks.
- **Mainstream Alignment:** Conforms to OpenAI, Anthropic, and standard LLM function-calling workflows.
- **Focus:** Engineering focus shifts to high-impact core runtime stability (BPE tokens, graph history, telemetry).

### Negative / Trade-offs
- Commands running through tools must complete within the tool invocation timeout rather than running indefinitely in the background.
- Long-running shell commands will block the turn until finished or timed out.
