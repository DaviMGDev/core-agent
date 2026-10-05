---
type: spec
title: "model-manager — Plugin Specification"
description: "Manages models as views — alias, fallback chain, or discussion group — resolved in dependency order with cycles refused."
tags: [spec, plugin]
sections: [context, semantics, resolution, abi, conformance, non-goals, decisions]
created: "2026-10-05"
updated: "2026-10-05"
---

# model-manager — Plugin Specification

## Context

model-manager owns the model side of the system. A custom model is a view, not
a new model: an alias to another model, a fallback chain, or a discussion group
of several provider models that talk before one response is returned. The
plugin provides the key `model-registry` and injects `provider-registry`, so it
activates only when providers are available.

The first cut implements registration, resolution, and response selection. A
response reaches a provider: the guest resolves the concrete model to an
endpoint and a credential reference through the injected provider registry and
performs the exchange over the loader's host-mediated HTTP transport.
Orchestration of a discussion is still deferred.

## Semantics

A view is `{name, alias | fallback | discuss}`:

- `alias` names one target;
- `fallback` lists targets tried in order;
- `discuss` lists participants in speaking order;
- exactly one of the three must be set; `name` is non-empty and unique.

A name with no registered view resolves as a plain model (mode `model`,
targets `[name]`).

## Resolution

`Resolve(name)` returns `{mode, models}`:

- `model` → `[name]`; `alias` → the target's resolution, flattened;
- `fallback` → each target resolved and concatenated in listed order;
- `discuss` → each participant resolved and concatenated in listed order.

Resolution refuses cycles (including direct self-reference) with an error. The
guest resolves every registered name at activation, so a cyclic configuration
fails the load rather than a later lookup.

The discussion merge contract (deferred): each participant sees the running
transcript and the last response is the turn's response; until merge
orchestration lands, `Resolve` exposes exactly the ordered participant list
the contract needs.

- Operations: `resolve` (mode and flattened targets for a name) and `respond`
  (resolve the requested model and answer through the caller). A `fallback`
  view tries its targets in order and answers with the first that succeeds;
  an `alias` or a plain model answers with its one concrete model; a `discuss`
  group answers with its first participant, since merge orchestration is still
  deferred. A response without a caller is refused.

## ABI

- Exports: `memento_declare`, `memento_activate`, `memento_revert_effect`,
  `memento_alloc`, `memento_handle`, `memory`.
- Imports: `memento.declare_inject`, `memento.declare_provide`,
  `memento.bind`, `memento.get_len`, `memento.get`, `memento.http_request`,
  `memento.http_response_len`, `memento.http_response`,
  `memento.get_payload_len`, `memento.get_payload`, `memento.register_effect`,
  `memento.log`.
- Declares: provides `model-registry`; injects `provider-registry`.
- Binds: `model-registry` ← the activation payload (the view configuration
  document), as a tracked, revertible registration.
- Reads: `provider-registry` over `get_len`/`get` at activation, parsed into a
  local shape so the package stays independent of provider-manager.
- Transport: a response calls `http_request` with
  `{method, url, headers, body}` for `POST <endpoint>/chat/completions`,
  mapping context messages to `messages`; the credential crosses as an
  `env:VAR` reference the host substitutes.
- Payload: JSON `{"models":[{"name","alias"|"fallback"|"discuss"}]}`.
- Effect inverse on unload: `model-manager: model views released`.

## Conformance

Colocated Go tests cover registration validation, plain models, aliases,
fallback chains, discussion groups, flattening order, and cycle refusal. The
guest path is exercised by the `cmd/core-agent` end-to-end test. Scenarios:
[`features/model-manager.feature`](features/model-manager.feature).

## Non-Goals

Discussion merge orchestration; model metadata, pricing, or quotas; persistent
view storage; the transport itself, which the loader owns.

## Decisions

- **MM1 — Views, not models.** An alias, a chain, and a discussion group are
  compositions over provider models; no new model object is invented.
- **MM2 — Resolve eagerly at activation.** Cyclic or dangling views fail the
  load, keeping later lookups total.
- **MM3 — Discussion orchestration deferred, resolution fixed.** The
  participant order is part of this contract; merging several participants
  into one response is still deferred, while a single response performs a real
  provider call.
- **MM4 — The caller owns the exchange.** Resolution stays in the library; the
  guest implements `Caller` over the loader's HTTP transport, mapping a
  concrete model to a provider through the injected registry. The library
  never imports provider-manager.
