---
type: spec
title: "provider-manager — Plugin Specification"
description: "Manages AI providers: registration, validation, lookup, listing of served models, and the endpoint/model mapping model-manager uses for calls."
tags: [spec, plugin]
sections: [context, semantics, transport, abi, conformance, non-goals, decisions]
created: "2026-10-05"
updated: "2026-10-05"
---

# provider-manager — Plugin Specification

## Context

provider-manager owns the provider side of the system: it stores AI providers
(name, endpoint, credential reference, served models), validates them, keeps a
stable order, and serves lookups and the model-to-provider mapping. It
provides the key `provider-registry` and injects nothing.

The kernel now exposes the network through the loader's host-mediated
transport, so a chat turn reaches a provider. provider-manager stays
management-only: model-manager performs the exchange, resolving the concrete
model to an endpoint through the registry this plugin publishes.

## Semantics

- A provider is `{name, endpoint, credential, models, mock}`:
  - `name` is non-empty and unique in the registry;
  - `endpoint` is an absolute `http` or `https` URL with a host; required
    unless `mock` is set;
  - `credential` is a non-secret reference (`env:VAR` style), never a literal
    secret in the payload, substituted host-side at request time; it may be
    empty for an endpoint that needs no authentication (a local runtime), and
    a `mock` provider ignores it;
  - `models` lists the concrete model names the provider serves, in no
    particular order; it may be empty;
  - `mock`, when set, makes the provider answer in-process: it needs neither an
    endpoint nor a credential, and model-manager replies through its mock
    caller instead of performing an exchange.
- `Register` validates and appends; a duplicate name is refused with an error
  and the registry is unchanged.
- `List` returns providers in insertion order; `Get` and `Remove` address by
  name.
- `ProviderFor` returns the first provider, in insertion order, that lists
  the given concrete model.
- `ParseConfig` reads `{"providers":[...]}`; the guest registers every
  provider at activation and fails activation on the first invalid one.

## Transport

The kernel exposes the capability this plugin once proposed:
`memento.http_request` and its `http_response_len`/`http_response` pair, with
host-owned egress policy and host-substituted credential references. The
proposal is no longer pending; the ABI is documented in memento's
`plugins/wasm/specs/`.

provider-manager does not perform requests. It publishes the registry that
model-manager reads over `memento.get` (the injected `provider-registry`
document) to resolve a model to an endpoint and a credential reference. The
request itself belongs to the responder, which keeps this plugin a pure
management surface.

## ABI

- Exports: `memento_declare`, `memento_activate`, `memento_revert_effect`,
  `memory` (no operation handler: provider-manager is a pure management
  surface).
- Imports: `memento.declare_inject`, `memento.declare_provide`,
  `memento.bind`, `memento.get_payload_len`, `memento.get_payload`,
  `memento.register_effect`, `memento.log`.
- Declares: provides `provider-registry`; injects nothing.
- Binds: `provider-registry` ← the activation payload (the provider
  configuration document), as a tracked, revertible registration.
- Payload: JSON `{"providers":[{"name","endpoint","credential","models"}]}`.
- Effect inverse on unload: `provider-manager: providers released`.

## Conformance

Colocated Go tests cover registration, validation, duplicate refusal, listing
order, lookup, removal, the model-to-provider mapping, and config parsing. The
guest path is exercised by the `cmd/core-agent` end-to-end test. Scenarios:
[`features/provider-manager.feature`](features/provider-manager.feature).

## Non-Goals

Performing requests itself — the transport belongs to model-manager; storing
literal secrets; per-provider rate limiting or retries; provider health
checks.

## Decisions

- **PM1 — Transport landed upstream.** The `memento.http_request` extension is
  implemented in memento's loader; provider-manager publishes the registry
  model-manager uses and never reaches around the ABI.
- **PM2 — Credentials are references.** When present, payloads carry
  `env:VAR`-style references; the host substitutes them at request time, so
  specs, repositories, and guest memory stay secret-free. A keyless provider
  (a local runtime) simply carries none.
- **PM3 — Stable order.** Listing preserves insertion order, keeping the
  registry deterministic for tests and transcripts.
- **PM4 — Providers declare the models they serve.** `models` is the
  model-to-endpoint mapping; a concrete model that no provider lists is a
  dangling reference, and a call for it fails loudly.
- **PM5 — A mock provider is a provider.** Mocking the LLM is configuration,
  not a separate server: `mock` marks a provider that answers in-process, so
  the same composition runs with or without a real endpoint.
