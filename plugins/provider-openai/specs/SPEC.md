---
type: spec
title: "provider-openai — Plugin Specification"
description: "A provider plugin that registers an OpenAI-compatible provider into the live provider registry at activation and unregisters on unload."
tags: [spec, plugin]
sections: [context, semantics, lifecycle, abi, conformance, non-goals, decisions]
created: "2026-10-07"
updated: "2026-10-07"
---

# provider-openai — Plugin Specification

## Context

provider-openai is a provider plugin (issue #13): it connects to an
OpenAI-compatible endpoint by registering itself into the live
`provider-registry` at activation. It injects `provider-registry` and provides
nothing, participating in the composition purely through its lifecycle effect.

## Semantics

- The plugin represents one OpenAI-compatible provider: name, endpoint,
  credential reference, and served models.
- The default configuration registers provider `"openai"`, endpoint
  `"https://api.openai.com/v1"`, credential `"env:OPENAI_API_KEY"`, and models
  `["gpt-4o", "gpt-4o-mini"]`.
- A custom JSON payload overrides any of these fields.

## Lifecycle

- **Activation:** The guest invokes `provider-registry` with
  `{"op": "register", "provider": {...}}`. Upon success, it registers a
  memento effect for cleanup.
- **Unload (revert effect):** When deactivated or unloaded, the effect inverse
  invokes `provider-registry` with `{"op": "unregister", "name": ...}`,
  cleanly removing the registration.

## ABI

- Exports: `memento_declare`, `memento_activate`, `memento_revert_effect`,
  `memory`.
- Imports: `memento.declare_inject`, `memento.invoke`,
  `memento.get_payload_len`, `memento.get_payload`,
  `memento.register_effect`, `memento.log`.
- Declares: injects `provider-registry`; provides nothing.
- Payload: optional JSON
  `{"name":"...","endpoint":"...","credential":"...","models":[...]}`.
- Effect inverse on unload: unregisters the provider from `provider-registry`.

## Conformance

Colocated Go tests cover registration document creation, lifecycle handling,
and effect revert. Scenarios:
[`features/provider-openai.feature`](features/provider-openai.feature).

## Non-Goals

Managing models outside its own provider; direct HTTP transport (transport is
handled by model-manager); view resolution.

## Decisions

- **PO1 — Lifecycle registration.** The plugin injects `provider-registry` and
  registers via invoke at activation time, removing itself via an effect on
  unload.
- **PO2 — Payload defaults.** When no payload is provided, the plugin defaults
  to standard OpenAI endpoint and models.
