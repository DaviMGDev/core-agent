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
- `Subscribe(topic, waker)` appends a subscriber; subscription is
  host-configured (system D14).
- Delivery order per subscriber is publish order.
- A subscriber is woken by the host, never preempted: at most one wake is in
  flight per subscriber, and a wake that arrives while the subscriber is
  busy waits for it to return.
- One wake drains every event queued for the subscriber at that point:
  N events become one turn.

## Delivery

The bus owns a queue per subscription. Publishing marks a subscriber
scheduled and a delivery goroutine calls its `Wake(events)`; the waker (the
host) serializes the wake with the subscriber's own calls — the module lock
does the waiting, so no wake preempts a call in flight. Events published
during a wake are delivered in a later wake.

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
