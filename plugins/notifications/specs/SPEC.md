---
type: spec
title: "notifications — Plugin Specification"
description: "The queued event bus: publish/subscribe over job and chat topics, with host wakes that coalesce."
tags: [spec, plugin]
sections: [context, semantics, delivery, topics, conformance, non-goals, decisions]
created: "2026-10-06"
updated: "2026-10-06"
---

# notifications — Plugin Specification

## Context

notifications is the agent layer's event bus, host-side per system D14. Any
component publishes; any component subscribes to a topic; the host wakes a
subscriber when its module lock is free. It provides no guest key: guests
reach it through the loader's `publish` import (memento `plugins/wasm` spec
D11-D13), and `cmd/core-agent` injects it into the memento engine.

## Semantics

- An event is `{topic, payload}`; the payload is bytes.
- `Publish(topic, payload)` queues the event for every subscriber of the
  topic and returns to the publisher without blocking; a publisher never
  waits on a subscriber.
- `Subscribe(topic, waker)` appends a subscriber and returns its
  subscription; subscription is host-configured (system D14).
- A waker blocks until the subscriber can receive, then drains the queue with
  `Take`; the drain is what makes a wake.
- Delivery order per subscriber is publish order.
- A subscriber is woken by the host, never preempted: at most one wake is in
  flight per subscriber, and a wake that arrives while the subscriber is
  busy waits for it to return.
- One wake drains every event queued for the subscriber at that point:
  N events become one turn.
- `SubscribeClocked(topic, waker)` opts a subscription into the bus clock:
  its queued events flush together on each period boundary, a boundary with
  nothing queued wakes no one, and events published during a wake wait for
  the next boundary. `Flush` performs one boundary and `Close` stops the
  ticker; a bus without a period keeps every subscription immediate.
- A driver may wait for a subscription to drain (`WaitIdle`): the wait returns
  once no event is queued and no wake is in flight, so a caller can let a
  subscriber finish rendering before it continues.

## Delivery

The bus owns a queue per subscription. Publishing appends and marks the
subscriber scheduled; a delivery goroutine calls `Wake(sub)`, which blocks
until the subscriber can receive — its module lock is the waiting room — and
then drains with `sub.Take()`. Because the drain happens when the lock is
free, every event queued while the subscriber was busy lands in that one
wake, and no wake preempts a call in flight. Events published after the
drain are delivered in a later wake. `WaitIdle` closes the same bookkeeping:
the subscription is idle again only after the wake returned and the queue is
empty, which is what a session driver waits on to keep output ordered.

A clocked subscription swaps immediate delivery for the boundary: publishing
queues and marks the subscription scheduled, and `Flush` — the ticker calls
it on each period — spawns one wake for every clocked subscription whose
queue is non-empty. Events that arrive while a wake runs wait for the next
boundary, so a period's events travel together and no wake overlaps another.
An empty boundary spawns nothing, so an idle period wakes no one.

## Topics

The layer's well-known topics:

| Topic | Carries |
|---|---|
| `job.started` | the job handle and tool |
| `job.tick` | elapsed time — a clock nudge, not a health claim |
| `job.completed` | the result |
| `job.failed` | the error |
| `job.killed` | the kill |
| `chat.message` | a message for whoever renders the conversation |

A topic is a string; the table names the layer's vocabulary, not a closed
set.

## Conformance

Colocated Go tests cover queueing, ordering, coalescing, and wake
serialization; the guest publish path is covered by the loader's ABI tests
and the entry's end-to-end test. Scenarios:
[`features/notifications.feature`](features/notifications.feature).

## Non-Goals

Persistence; delivery guarantees across process restarts; wildcard or
pattern subscriptions; per-subscriber backpressure or quotas.

## Decisions

- **N1 — The publisher never blocks.** Publishing only queues and schedules;
  delivery is the host's work.
- **N2 — Wakes coalesce, per subscriber.** One wake drains the queue;
  ordering is publish order.
- **N3 — Wakes wait, never preempt.** The subscriber's module lock is the
  waiting room; a wake is a call like any other.
- **N4 — Topics are vocabulary, not a closed set.** The named topics are the
  layer's shared language; the bus itself carries any string.
- **N5 — The clock batches, it never hides.** A clocked subscription's
  non-empty queue flushes as one wake on each boundary; an empty boundary
  wakes no one; and the wake is a normal wake — the bus drops no event and
  suppresses no subscriber. Immediate subscriptions are never delayed by the
  clock.
