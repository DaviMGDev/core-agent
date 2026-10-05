---
type: spec
title: "provider-manager — Plugin Specification"
description: "Manages AI providers: registration, validation, lookup, and listing; names the upstream ABI extension its transport needs."
tags: [spec, plugin]
sections: [context, semantics, transport, abi, conformance, non-goals, decisions]
created: "2026-10-05"
updated: "2026-10-05"
---

# provider-manager — Plugin Specification

## Context

provider-manager owns the provider side of the system: it stores AI providers
(name, endpoint, credential reference), validates them, keeps a stable order,
and serves lookups. It provides the key `provider-registry` and injects
nothing.

Calling a provider needs HTTP. The current kernel ABI has no network import, so
— per the charter's default — this spec answers the gap with an upstream
proposal instead of a local workaround. The first cut implements management
semantics only.

## Semantics

- A provider is `{name, endpoint, credential}`:
  - `name` is non-empty and unique in the registry;
  - `endpoint` is an absolute `http` or `https` URL with a host;
  - `credential` is a non-secret reference (`env:VAR` style), never a literal
    secret in the payload.
- `Register` validates and appends; a duplicate name is refused with an error
  and the registry is unchanged.
- `List` returns providers in insertion order; `Get` and `Remove` address by
  name.
- `ParseConfig` reads `{"providers":[...]}`; the guest registers every
  provider at activation and fails activation on the first invalid one.

## Transport

The first cut does not perform requests. The transport needs a kernel
capability, proposed upstream to memento (not patched here):

- **Proposal — `memento.http_request`.** The guest writes a JSON request
  `{method, url, headers, body}` into its memory; the host performs the
  request and writes a JSON response `{status, headers, body}` back; the call
  returns `0` on a completed exchange and non-zero on transport failure. The
  host owns egress policy (timeouts, allow-lists).

Until the proposal lands, a chat turn cannot reach a provider; that is a
declared limitation, not a silent one.

## ABI

- Exports: `memento_declare`, `memento_activate`, `memento_revert_effect`,
  `memory` (no operation handler: transport is deferred).
- Imports: `memento.declare_inject`, `memento.declare_provide`,
  `memento.bind`, `memento.get_payload_len`, `memento.get_payload`,
  `memento.register_effect`, `memento.log`.
- Declares: provides `provider-registry`; injects nothing.
- Binds: `provider-registry` ← the activation payload (the provider
  configuration document), as a tracked, revertible registration.
- Payload: JSON `{"providers":[{"name","endpoint","credential"}]}`.
- Effect inverse on unload: `provider-manager: providers released`.

## Conformance

Colocated Go tests cover registration, validation, duplicate refusal, listing
order, lookup, removal, and config parsing. The guest path is exercised by the
`cmd/core-agent` end-to-end test. Scenarios:
[`features/provider-manager.feature`](features/provider-manager.feature).

## Non-Goals

Performing HTTP requests in the first cut; storing literal secrets;
per-provider rate limiting or retries; provider health checks.

## Decisions

- **PM1 — Transport deferred, not patched.** Network arrives as the upstream
  `memento.http_request` proposal; provider-manager never reaches around the
  ABI.
- **PM2 — Credentials are references.** Payloads carry `env:VAR`-style
  references so specs and repositories stay secret-free.
- **PM3 — Stable order.** Listing preserves insertion order, keeping the
  registry deterministic for tests and transcripts.
