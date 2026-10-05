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

The first cut implements registration and resolution. The discussion itself
requires model calls, so its orchestration waits for the same transport
extension provider-manager proposes.

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
transcript and the last response is the turn's response; until transport
lands, `Resolve` exposes exactly the ordered participant list the contract
needs.

- Operations: `resolve` (mode and flattened targets for a name) and `respond`
  (resolve the requested model and answer with its first concrete model; the
  response text is `[<model>] <text> (context:<n>)`, where `<n>` is the
  supplied context size). Discussion orchestration still waits for transport.

## ABI

- Exports: `memento_declare`, `memento_activate`, `memento_revert_effect`,
  `memento_alloc`, `memento_handle`, `memory`.
- Imports: `memento.declare_inject`, `memento.declare_provide`,
  `memento.bind`, `memento.get_payload_len`, `memento.get_payload`,
  `memento.register_effect`, `memento.log`.
- Declares: provides `model-registry`; injects `provider-registry`.
- Binds: `model-registry` ← the activation payload (the view configuration
  document), as a tracked, revertible registration.
- Payload: JSON `{"models":[{"name","alias"|"fallback"|"discuss"}]}`.
- Effect inverse on unload: `model-manager: model views released`.

## Conformance

Colocated Go tests cover registration validation, plain models, aliases,
fallback chains, discussion groups, flattening order, and cycle refusal. The
guest path is exercised by the `cmd/core-agent` end-to-end test. Scenarios:
[`features/model-manager.feature`](features/model-manager.feature).

## Non-Goals

Calling models in the first cut; discussion orchestration before transport;
model metadata, pricing, or quotas; persistent view storage.

## Decisions

- **MM1 — Views, not models.** An alias, a chain, and a discussion group are
  compositions over provider models; no new model object is invented.
- **MM2 — Resolve eagerly at activation.** Cyclic or dangling views fail the
  load, keeping later lookups total.
- **MM3 — Discussion deferred, resolution fixed.** The participant order is
  part of this contract now; the merge step arrives with transport.
