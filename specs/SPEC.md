---
type: spec
title: "core-agent — System Specification"
description: "An agent core on memento: the kernel imported as-is, every other capability a plugin composed at runtime."
tags: [spec]
sections: [context, users, user-stories, architecture, semantics, conformance, nfr, non-goals, decisions]
created: "2026-10-05"
updated: "2026-10-05"
---

# core-agent — System Specification

*This spec declares its own contract: every `##` section below appears in
`sections:` in the frontmatter, and nothing appears undeclared. `context` and
`decisions` are mandatory; the rest fit this project's kind (application
system).*

## Context

core-agent is an agent core built on [memento](https://github.com/DaviMGDev/memento),
following spatiotemporal composability — the paradigm memento implements
("A Programming Paradigm for Spatiotemporal Composability", arXiv:2608.25512).
The kernel is memento, imported as-is; every other capability is a plugin,
loaded dynamically as a `.wasm` guest through memento's loader on wazero.

The first cut is deliberately small: five starter plugins, nothing fancy, no
extras until these run. The LLM reaches the system through the REPL, like any
other user.

Composition is the point. Plugins declare the context keys they inject and
provide; memento activates a plugin only when its keys are satisfied,
deactivates it when a provider withdraws or is replaced, and reclaims every
effect the plugin installed — no hand-written teardown. A plugin can therefore
arrive at runtime, not only at build time.

The system's contract lives here, with executable Gherkin scenarios in
[`features/`](features/). Plugin contracts live beside their plugins
(`plugins/<name>/specs/`), per memento's convention that root specifications
stay plugin-independent.

## Users

**LLM user** — reaches the system through the REPL: sends lines, reads
responses, ends the session with `:quit`. Drives exactly the session a human
drives; no privileged channel.

**Plugin author** — writes one plugin directory: a host-testable Go library, a
wasip1 guest built to `.wasm`, local specs, and tests beside the code. Expects
the kernel to own composition and reclamation.

**System assembler** — writes entries under `cmd/` that register plugins and
reconcile a loader tree with payloads.

## User Stories

**US-001 — The system composes the five starter plugins**

As a system assembler,
I want to declare the five plugins as loader entries,
so that the kernel activates them in dependency order and unloads them
completely.

Acceptance criteria (EARS):

- WHEN the five entries are reconciled THE system SHALL activate each plugin
  once its declared keys are satisfied. (Event-driven)
- WHEN the tree is emptied THE system SHALL unload each plugin and run its
  effect inverse. (Event-driven)
- IF two components provide the same key THEN the kernel SHALL refuse the
  second with an error and leave the composition unchanged. (Unwanted behavior)

**US-002 — A REPL session**

As an LLM user,
I want to drive the system through a REPL,
so that I can use the composed capabilities like any other user.

Acceptance criteria (EARS):

- WHEN the session starts THE REPL SHALL announce the nickname and print a
  `you> ` prompt. (Event-driven)
- WHEN the line is `:help` THE REPL SHALL list the basic commands. (Event-driven)
- WHEN the line is `:quit` (or `:q`, `:exit`) THE REPL SHALL end the session.
  (Event-driven)
- WHEN the line is empty or blank THE REPL SHALL print the next prompt without
  a response. (Event-driven)
- WHEN the line is an ordinary chat line THE REPL SHALL return exactly one
  response line for the turn. (Event-driven)

**US-003 — Management surfaces behind the plugins**

As a plugin author,
I want provider, model, history, and context state managed by small,
spec-backed plugin libraries,
so that each behavior is testable beside its package and composition stays
separate from logic.

Acceptance criteria (EARS):

- The provider registry SHALL validate and store providers (name, endpoint,
  credential reference). (Ubiquitous)
- The model registry SHALL resolve alias, fallback-chain, and discussion
  views, refusing cycles. (Ubiquitous)
- Chat history SHALL record messages and serve recent turns. (Ubiquitous)
- The context manager SHALL project history into a bounded window. (Ubiquitous)

**US-004 — Specs precede source**

As a maintainer,
I want the contract written and confirmed before source,
so that the implementation has a stable target.

Acceptance criteria (EARS):

- IF a behavior has no spec THE author SHALL NOT implement it. (Unwanted
  behavior)

## Architecture

**Kernel.** memento is imported as-is: `context/` (typed keys, bindings,
revertible effects), `runtime/` (fibers, scheduler, reactive coeffects),
`loader/` (entry tree, factory registry, reconciliation), and `plugins/wasm`
(wazero engine, host ABI, guest lifecycle). Changes go upstream; memento's
root specs are never touched for plugin reasons.

**Plugins.** Each plugin is one directory: `plugins/<name>/` holds the
host-testable Go library and its tests, `plugins/<name>/guest/` holds the
wasip1 `package main` guest, `plugins/<name>/specs/` holds the plugin's
contract. The guest is built with
`GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared` and committed beside the
plugin; the host side embeds it.

**Host ABI (kernel surface).** Guests export `memento_declare`,
`memento_activate`, `memento_revert_effect`, and `memory`, and may export
`memento_alloc`/`memento_handle` to serve invocations; they import
`memento.declare_inject`, `memento.declare_provide`, `memento.bind`,
`memento.get_len`, `memento.get`, `memento.invoke`, `memento.get_payload_len`,
`memento.get_payload`, `memento.register_effect`, and `memento.log`. Strings
cross as (pointer, length) pairs into guest memory; the ABI's isolated
contract lives in memento's `plugins/wasm/specs/`. WASI stdio is wired for the
REPL guest.

**Composition (starter keys).** One shared key registry maps guest key names to
memento keys:

| Plugin | Injects | Provides |
|---|---|---|
| provider-manager | — | `provider-registry` |
| model-manager | `provider-registry` | `model-registry` |
| chat-history | — | `chat-history` |
| context-manager | `chat-history` | `llm-context` |
| repl-chat | `chat-history`, `model-registry`, `llm-context` | `repl` |

**Entry.** `cmd/core-agent` registers the five compiled guests, composes them
through the scheduler (`Insert` / `Inspect` / `Remove`), hosts the REPL
session, and unloads on exit. The scheduler path is deliberate: the REPL's
activation hosts the interactive session, which outlives the loader's
five-second quiescence window (memento documents the pattern in
`examples/chat`); reconciliation stays the kernel mechanism for non-blocking
compositions.

**Registration.** A guest registers a provided key by binding its value during
activation (`memento.bind`): the kernel installs the binding as a revertible
fiber effect and advertises the key once the fiber owns it. A declared
provide left unbound fails activation. The extended ABI (bind/get/invoke)
landed upstream in memento's `plugins/wasm`, so the entry carries no adapter.

**Sources of truth.** The charter is `init.pseudo`; this spec refines it.
Plugin specifics live in the plugin specs.

## Semantics

**Activation.** A plugin activates only when every injected key has an active
provider; a withdrawn or replaced provider deactivates dependents first. The
kernel refuses a second provider of the same key and refuses dependency cycles.

**Effect discipline.** Every guest registers at least one effect during
activation, binds its declared provided keys (the registration is itself one
of those tracked effects), and implements `memento_revert_effect`; unloading
replays inverses in LIFO order, and the host closes the module instance after
the guest's inverses run. No guest writes cleanup paths outside this
mechanism.

**REPL protocol.** `you> ` before each read; a response line after each
non-command turn; `:help`, `:quit` (aliases `:q`, `:exit`) are commands; blank
lines produce a new prompt only. Lines come from WASI stdin; output goes
through the kernel log import; the session ends on `:quit` or EOF, and unload
closes it.

**Turn pipeline.** A chat turn flows through the composed plugins over the
loader's `invoke` ABI: the REPL appends the user turn to chat-history, reads
recent turns, projects them into the context window, asks model-manager to
respond (which resolves the model view to its first concrete model), and
records the assistant reply. With no transport yet (D6), the response text is
deterministic — `[<model>] <text> (context:<turns>)` — so the whole pipeline is
visible in the transcript; only the provider call is missing.

**Management semantics.** provider-manager stores `{name, endpoint,
credential}` with validation and stable listing; model-manager resolves `alias`
views to one target, `fallback` chains to ordered targets, and `discuss` groups
to ordered participants, refusing cycles; chat-history appends role-tagged
messages and serves the most recent turns; context-manager projects a message
list into a budgeted window (newest first, at least the newest message kept).
Each management plugin serves its surface as ABI operations through its
handler: chat-history `append`/`recent`, context-manager `project`,
model-manager `resolve`/`respond`.

**Configuration.** Each entry carries a payload: JSON config for the
management plugins, a nickname string for repl-chat. The assembler provides
defaults in `cmd/` and may override them with entries.

## Conformance

The Gherkin scenarios in [`features/`](features/) are normative. The first cut
executes behavior through Go tests colocated with each package, plus the
end-to-end test in `cmd/core-agent` that scripts stdin through the composed
system:

```console
$ go test ./...
```

Godog remains the assumed runner (memento convention); a conformance entry
that runs these features on Godog is deferred until the first cut runs (D7).

## Non-Functional Requirements

- **Determinism.** Given the same tree and transcript, the log transcript
  contains the same lines; tests assert content, not timing.
- **Isolation.** A plugin's library never imports another plugin's library;
  shared behavior crosses through kernel keys and payloads.
- **Reclamation.** After unloading the tree, no fiber and no guest module
  remains.
- **Portability.** The host builds with Go 1.23+; guests build with the wasip1
  port; no cgo.

## Non-Goals

Extras beyond the five starter plugins; local patches to memento (changes go
upstream); network or binding-read guest capabilities before the upstream ABI
extension; loading or unloading plugins from inside the REPL; persistence of
history or credentials; cross-process or out-of-tree composition; any layout
beyond `plugins/` and `cmd/` in the first cut.

## Decisions

- **D1 — Kernel as-is.** memento is imported, not vendored or patched; missing
  capabilities become upstream proposals, tracked in the plugin specs that
  need them.
- **D2 — Plugin form: wasm guest first.** repl-chat is a wasm guest from day
  one (WASI stdio fits it); management plugins are wasm guests too, with their
  host-testable logic in the plugin library. (Charter open question 1.)
- **D3 — Spec placement.** System spec here; plugin-local specs under
  `plugins/<name>/specs/`, as memento keeps root specs plugin-independent.
  (Charter open question 2.)
- **D4 — LLM in the REPL.** The LLM drives the same REPL a human drives. The
  REPL does not load or unload plugins in the first cut: composition is the
  loader's job, exercised from `cmd/`; repl-chat's spec records this decision.
  (Charter open question 3.)
- **D5 — Shared key registry.** All five components register against one
  `wasm.KeyRegistry`, so injection satisfaction and provider identity work
  across plugins; each guest binds its provided values through the ABI, so the
  registration is the guest's own tracked effect.
- **D6 — Transport deferred, not patched.** The current host ABI exposes no
  network calls; provider-manager specifies the required upstream extension
  (`memento.http_request`, proposed to memento) and implements management
  semantics only. No silent workaround.
- **D7 — Conformance now, Godog next.** Gherkin is authored as the contract
  now; executable conformance in the first cut is the colocated Go test suite
  plus the `cmd/core-agent` end-to-end test, because the layout first cut is
  `plugins/` + `cmd/` only (a conformance entry needs a layout proposal) and no
  dependency is added before a spec needs it. A Godog runner over these
  features is the immediate follow-up.
- **D8 — Cross-guest data flow runs over invoke.** The loader's `invoke` ABI
  is the call surface: the REPL pipeline reaches chat-history,
  context-manager, and model-manager through their operation handlers, so the
  first cut composes live data, not only lifecycle. Transport remains the only
  deferred provider capability (D6).
- **D9 — Committed artifacts.** Each plugin's `.wasm` is built from
  `plugins/<name>/guest` and committed beside the plugin; the host side embeds
  it, so tests and the entry need no rebuild step.
- **D10 — Payloads.** JSON for configurable plugins, string for the REPL
  nickname; defaults live in `cmd/core-agent` and are overridable by entries.
- **D11 — Provide binding is the guest's own effect.** The loader gained
  `bind`/`get`/`invoke` upstream (memento `plugins/wasm`); each guest binds its
declared provides during activation through `memento.bind`, and the temporary
host-side binding adapter was removed. Nothing about registration is
host-side anymore.
