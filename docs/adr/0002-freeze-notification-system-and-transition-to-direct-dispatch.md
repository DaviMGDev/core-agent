# ADR 0002: Freeze Notification Subsystem and Transition to Direct Synchronous Dispatch

- **Status:** Accepted
- **Date:** 2026-10-13
- **Deciders:** Core Agent Team
- **Updates / Extends:** [ADR 0001: Freeze Background Job System](0001-freeze-job-system-and-return-to-sync-tools.md)

---

## Context

Following the background job freeze in [ADR 0001](0001-freeze-job-system-and-return-to-sync-tools.md), the `plugins/notifications` event bus remained partially active in the core runtime to transport `chat.message` events and periodic clock batch boundaries (`clock.period_ms`).

However, maintaining an asynchronous queued event bus with clock tickers, module locks, and background wakers introduces unnecessary concurrency overhead and latency for what is now a purely synchronous, blocking conversational loop:
1. **Clock Batching Latency:** The notification clock ticker artificially deferred immediate chat turns and UI stream updates to boundary windows (`clock.period_ms`), slowing down conversational interaction.
2. **Background Wake Complexity:** Asynchronous goroutine wakers (`deliver`, `deliverClocked`) and channel locks add race condition risks and debugging friction without providing concurrency benefits, since background jobs are already frozen.
3. **Semantic Decoupling:** Client messaging (`cmd/core-agent/main.go` and `internal/link`) can publish messages directly and synchronously to stdout or Link streams without going through an intermediate pub/sub memory queue.

---

## Decision

We decide to:
1. **Freeze the notification subsystem:**
   - Pause the asynchronous notification queue (`plugins/notifications/notifications.go`), background goroutines (`runClock`, `deliver`), and periodic clock boundary flushes (`Flush`).
   - Enable direct synchronous message dispatch by default in `cmd/core-agent`.
   - Gate the legacy queued notification bus behind a `-legacy-notifications` or `-legacy-bus` CLI flag for opt-in execution.
2. **Decouple streaming and intermediate thoughts from notifications:**
   - Turn loop intermediate progress indicators and reasoning chunks stream directly over standard Link stream chunks and writer channels, bypassing the notification bus completely.
3. **Decouple TUI client events from notification semantics:**
   - TUI notices (such as connection status and link drops) are treated strictly as client-side UI presentation notices, decoupled from bus topics.
4. **Preserve notification architecture for future revival:**
   - Retain `plugins/notifications` source and tests intact.
   - When background long-running jobs are eventually revived per ADR 0001, the queued event bus and clock batching can be revived in tandem.

---

## Affected Issues & Status

| Issue | Title | Action under Freeze |
|---|---|---|
| **#2** | Notifications: a queued event bus with job/chat topics | Frozen; bus queue and clock inactive by default |
| **#14** | Notification clock wake batching | Dormant during freeze; clock tickers bypassed |
| **#40** | Turn Loop: Streaming intermediate thoughts | Refocused onto direct Link stream chunks (no bus) |
| **#43** | Discussion: Dual-meaning 'system' role split across core and TUI | Refocused onto local UI `notice` role (no bus events) |
| **#44** | Discussion: Turn termination on explicit silence | Formally deferred (`deferred:job-system`, `deferred:notification-system`) |

---

## Consequences

### Positive
- **Determinism:** Eliminates asynchronous bus goroutines, tickers, and channel synchronization locks from the primary interactive loop.
- **Lower Latency:** Direct synchronous dispatch delivers messages immediately without waiting for clock boundary flushes.
- **Unified Architecture:** Aligns completely with the synchronous blocking model established in ADR 0001.

### Negative / Trade-offs
- Multiple decoupled subscribers cannot passively eavesdrop on turns without an explicit multicast Link tap.
