---
type: spec
title: "model-manager — Plugin Specification"
description: "Manages models as views — alias, fallback chain, or discussion group — resolved in dependency order with cycles refused; provider-native tool calls over the OpenAI-compatible tools array."
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
performs the exchange over the loader's host-mediated HTTP transport; a
provider marked `mock` answers in-process through `MockCaller` instead, so a
composition runs with no network. Orchestration of a discussion is still
deferred.

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
- `respond` carries the agent's presented tool surface as `tools` — a JSON
  wire shape of `{name, description, parameters}` per callable — and hands it
  to the caller unchanged, so the provider can offer the tools natively. The
  package keeps its own shape and never imports the agent's library.
- A provider answer carrying `tool_calls` normalizes to the first call as the
  text `{"tool": <name>, "args": <arguments>}` — the agent's existing
  directive — so a native call runs through the same path as a text
  directive. A `tool_calls` answer yields at most one call; further calls in
  the same answer are dropped.

## ABI

- Exports: `memento_declare`, `memento_activate`, `memento_revert_effect`,
  `memento_alloc`, `memento_handle`, `memory`.
- Imports: `memento.declare_inject`, `memento.declare_provide`,
  `memento.bind`, `memento.invoke`, `memento.http_request`,
  `memento.http_response_len`, `memento.http_response`,
  `memento.get_payload_len`, `memento.get_payload`, `memento.register_effect`,
  `memento.log`.
- Declares: provides `model-registry`; injects `provider-registry`.
- Binds: `model-registry` ← the activation payload (the view configuration
  document), as a tracked, revertible registration.
- Reads: `provider-registry` over `invoke` on every response: the
  `provider-for` operation maps the concrete model to its provider,
  decoded into a local shape so the package stays independent of
  provider-manager. No snapshot is kept — a provider registered after
  activation serves the next response, and a model no provider serves
  fails naming the model.
- Transport: a response calls `http_request` with
  `{method, url, headers, body}` for `POST <endpoint>/chat/completions`,
  mapping context messages to `messages`. When the provider carries a
  credential, it crosses as an `env:VAR` reference the host substitutes and is
  sent as `Authorization: Bearer`; a keyless provider sends no Authorization
  header. A provider error is reported with its status and body, so the
  failure reaches the transcript.
- Tools: when `respond` carries a non-empty surface, the request body
  includes the OpenAI-compatible `tools` array — one
  `{"type": "function", "function": {name, description, parameters}}`
  entry per presented callable. The field is included only when the surface
  is non-empty and omitted otherwise, so providers without tool support see
  the same request as before.
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
- **MM5 — A mock caller answers without a provider.** `MockCaller` implements
  the same `Caller` seam and echoes the model, the last user message, and the
  context size. A provider marked `mock` routes to it in the guest, so the
  LLM is mocked by config alone, with no network and no server.
- **MM6 — Native calls normalize to the directive.** The provider's
  `tool_calls` answer becomes the text directive `{"tool", "args"}` inside
  model-manager, so the agent's parse is the single call path and the
  text-directive path stays for mock and scripted providers. One answer, one
  call: the first tool call stands, the rest drop.
- **MM7 — Live resolution.** The guest resolves the provider per response
  through the registry's `provider-for` operation instead of the activation
  snapshot, so late-registered providers serve calls and withdrawn ones fail
  loudly. The injected key is still declared: `invoke` requires it.
