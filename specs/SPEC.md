---
type: spec
title: "core-agent — System Specification"
description: "An agent core on memento: the kernel imported as-is, every other capability a plugin composed at runtime."
tags: [spec]
sections: [context, users, user-stories, architecture, semantics, tui-link, configuration, conformance, nfr, non-goals, decisions]
created: "2026-10-05"
updated: "2026-10-10"
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
The kernel is memento, imported as-is; every other capability is a plugin
composed at runtime — the seven defaults as native components, with the
wasm guests kept in the tree as the unexercised dynamic-extension route.

The cut is deliberately small: seven starter plugins, and the agent layer adds
the pieces around the agent that owns the turn loop (the terminal invokes it
and renders its `chat.message` wakes; the tool manager, the notification bus,
and the subagent runner are host-side, D14). The LLM reaches the system
through the REPL, like any other user.

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

**Plugin author** — writes one plugin directory: a host-testable Go library,
a native component beside it, local specs, and tests beside the code. A
wasip1 guest stays optional: the committed guests are the frozen extension
route. Expects the kernel to own composition and reclamation.

**System assembler** — writes entries under `cmd/` that register plugins and
reconcile a loader tree with payloads.

## User Stories

**US-001 — The system composes the seven plugins**

As a system assembler,
I want to declare the seven plugins as loader entries,
so that the kernel activates them in dependency order and unloads them
completely.

Acceptance criteria (EARS):

- WHEN the seven entries are reconciled THE system SHALL activate each plugin
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
- WHEN a `chat.message` event arrives while no user line is pending THE
  terminal SHALL render it. (Event-driven)

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

**US-005 — The agent layer**

As an agent,
I want every call to be a job, events to wake me, and subagents to be
callable as tools,
so that I can act, hear back, and delegate.

Acceptance criteria (EARS):

- WHEN a tool is called THE manager SHALL create a job (queued → running →
  done | failed) and return a handle at once; `peep` observes it and `kill`
  is accepted at any point, honored at the next safe point. (Event-driven)
- WHEN a job emits an event THE bus SHALL queue it and wake its subscriber
  without blocking the publisher; one wake SHALL carry every queued event.
  (Event-driven)
- WHEN the model answers with a tool call THE agent's turn SHALL end; the
  job's completion or a tick SHALL wake the next turn. (Event-driven)
- WHEN a subagent is called THE manager SHALL run it as a job whose child
  works on its own conversation and model; the reply SHALL come back, and a
  call past the depth bound SHALL fail like any failed job. (Event-driven)
- WHEN a job emits events THE parent SHALL be the sole listener; a caller
  SHALL see and control its own subtree only. (Ubiquitous)

## Architecture

**Kernel.** memento is imported as-is: `context/` (typed keys, bindings,
revertible effects), `runtime/` (fibers, scheduler, reactive coeffects),
`loader/` (entry tree, factory registry, reconciliation), and `plugins/wasm`
(wazero engine, host ABI, guest lifecycle). Changes go upstream; memento's
root specs are never touched for plugin reasons.

**Plugins.** Each plugin is one directory: `plugins/<name>/` holds the
host-testable Go library and its tests, `plugins/<name>/guest/` holds the
wasip1 `package main` guest, `plugins/<name>/specs/` holds the plugin's
contract. The seven defaults are native components with the same operation
and wake documents the guests serve; the guests stay committed beside the
plugins as the frozen dynamic-extension route — neither built nor exercised
by the suite (`cmd/build-plugins.sh` remains for that route). Native
components call each other over typed Go contracts from one shared host-side
key set (`internal/keys`); the key names match the historical provide/inject
strings.

Host-side components follow the same shape — library and `specs/` under
`plugins/<name>/` — and run in the host process: the tool manager serves as
the agent's job service, the notification bus sits behind the publish path,
and the subagent runner registers as a manager tool over the agent loop.
Wasm guests, when composed as extensions, reach the host side only through
the ABI imports.

**Host ABI (kernel surface).** Guests export `memento_declare`,
`memento_activate`, `memento_revert_effect`, and `memory`, and may export
`memento_alloc`/`memento_handle` to serve invocations; they import
`memento.declare_inject`, `memento.declare_provide`, `memento.bind`,
`memento.get_len`, `memento.get`, `memento.invoke`, `memento.get_payload_len`,
`memento.get_payload`, `memento.http_request`, `memento.http_response_len`,
`memento.http_response`, `memento.job_start`, `memento.job_peep`,
`memento.job_kill`, `memento.job_result_len`, `memento.job_result`,
`memento.publish`, `memento.cancel_poll`, `memento.register_effect`, and
`memento.log`. Strings cross as (pointer, length) pairs into guest memory; the
ABI's isolated contract lives in memento's `plugins/wasm/specs/`. Native
components expose the same handler surface in-process (`Handle`), so the
host wakes the terminal and the agent with one batch of events at a time;
the entry owns stdin. Wasm guests export `memento_alloc`/`memento_handle`
for the same purpose, and no guest reads WASI stdio.

**Composition (starter keys).** One shared host-side key set
(`internal/keys`) carries the typed contracts; the key names are unchanged
from the guest era:

| Plugin | Injects | Provides |
|---|---|---|
| provider-manager | — | `provider-registry` |
| model-manager | `provider-registry` | `model-registry` |
| chat-history | — | `chat-history` |
| context-manager | `chat-history` | `llm-context` |
| agent | `chat-history`, `model-registry`, `llm-context` | `agent-loop` |
| repl-chat | `agent-loop` | `repl` |

**Entry.** `cmd/core-agent` registers the seven native components, composes them
through the scheduler (`Insert` / `Inspect` / `Remove`), wires the agent
layer — the tool manager as the host job service, the subagent tool over
the agent loop, a waker per subscriber — drives the terminal session over
the repl-chat library, and unloads on exit (reclaiming the jobs first). The
scheduler path is deliberate: an interactive session outlives the loader's
five-second quiescence window (memento documents the pattern in
`examples/chat`), and the session loop is host-driven — reading stdin and
receiving a bus wake cannot both live in a serialized guest — so
reconciliation stays the kernel mechanism for non-blocking compositions.

**Registration.** A component registers a provided key by binding its value
during
activation (`runtime.Bind` for native components, `memento.bind` for
guests): the kernel installs the binding as a revertible
fiber effect and advertises the key once the fiber owns it. A declared
provide left unbound fails activation. The extended ABI (bind/get/invoke)
landed upstream in memento's `plugins/wasm`, so the entry carries no adapter.

**Sources of truth.** The charters are `agent.pseudo` and `tui.pseudo` (both
kept outside version control); this spec refines them. Component specifics
live in the component specs.

## Semantics

**Activation.** A plugin activates only when every injected key has an active
provider; a withdrawn or replaced provider deactivates dependents first. The
kernel refuses a second provider of the same key and refuses dependency cycles.

**Effect discipline.** Every component registers at least one effect during
activation, binds its declared provided keys (the registration is itself one
of those tracked effects), and reverts through the kernel's LIFO inverses;
guests implement `memento_revert_effect`, and the host closes the module
instance after a guest's inverses run. No component writes cleanup paths
outside this mechanism.

**REPL protocol.** `you> ` before each read; a response line after each
non-command turn; `:help`, `:quit` (aliases `:q`, `:exit`) are commands; blank
lines produce a new prompt only. The host entry runs the loop over the
repl-chat library: it reads stdin, emits the prompt, hands each chat line to
the terminal, and waits for that turn's `chat.message` to render before
the next prompt. A message published while the session waits renders through
the same wake. The session ends on `:quit` or EOF, and unload closes it.

**Turn pipeline.** A chat turn flows through the composed plugins over typed
in-process contracts (the same operation documents the handlers serve): the terminal hands the line to the agent, which
appends the user turn to chat-history, reads recent turns, projects them into
the context window, asks model-manager to respond under one assembled system
message — identity, capabilities, honesty, wake and failure policy (agent
spec), transported untouched — and records the assistant reply. When it speaks, the agent publishes the message as `chat.message`;
the terminal renders the event, so a prompted reply and an unprompted
message take the same path. A tool call ends the turn without a message: the
agent starts a job and the job's completion, failure, or tick wakes the next
turn. model-manager resolves the view to
its concrete models, maps the chosen model to a provider through the injected
provider registry, and performs the exchange over plain HTTP with host-side
credential substitution; the provider's answer is the response line. A fallback view
tries its targets in order; a discussion group answers with its first
participant, since merge orchestration is still deferred. A provider marked
`mock` answers in-process — the mock caller echoes the model, the last user
message, and the context size — so the system runs with no provider and no
socket. Credentials cross as `env:VAR` (or `auth:NAME`) references the host
substitutes, so a reference crosses and never a secret.

**Jobs and notifications.** A tool call is a job, always: the manager's
registry holds the callables (`{name, description, argument schema,
runner}`), `job_start` returns a handle at once, `job_peep` reports state,
last tick, and output so far, and `job_kill` is accepted at any point and
honored at the next safe point — the calling job's host imports fail with the
canceled code, and a killed job's descendants are killed with it. A running
job emits `job.tick` on its interval (the caller's, else 30s) — a clock
nudge, never a health claim — and its end emits `job.completed` or
`job.failed`; reclamation on unload emits nothing. The bus queues every
event and wakes subscribers without blocking the publisher; one wake carries
every event queued at that point. The entry subscribes the agent's wake
through the notification clock (settings `clock.period_ms`, default 500):
queued job events flush together as one wake on each period boundary, a
boundary with nothing queued wakes no one, and events that arrive during a
wake wait for the next boundary. State transitions still stream to an
observing surface as they happen, and nothing the agent experiences is
suppressed — a batched tick wake is a normal turn whose speech crosses as
any other message. Events route by the job tree: a root job's events publish
on the bus, a child's go to its parent's listener and nowhere else. The entry
wakes the agent for its root jobs' ticks, completions, failures, and kills;
the agent decides whether to speak.

**Subagents.** A subagent is an agent the manager can call: the `subagent`
tool's runner invokes the same agent loop with the child's own conversation,
model, and budget, and completes when the child speaks — returning the final
reply or, when asked, the whole conversation. The child sees only the brief;
its turns are private. While the runner invokes the child, the job is
attributed to the caller's instance, so a kill unwinds the turn at its next
safe point and jobs the child starts become its descendants; the job tree is
bounded by configuration (default depth 2). Visibility and kill are
subtree-only.

**Management semantics.** provider-manager stores `{name, endpoint,
credential}` with validation and stable listing; model-manager resolves `alias`
views to one target, `fallback` chains to ordered targets, and `discuss` groups
to ordered participants, refusing cycles; chat-history appends role-tagged
messages and serves the most recent turns; context-manager projects a message
list into a budgeted window (newest first, at least the newest message kept).
Each management plugin serves its surface as operation documents through its
handler — and, natively, through its typed contract: chat-history `append`/`recent`,
context-manager `project`, model-manager `resolve`/`respond`.

**Configuration.** Each entry carries a payload: JSON config for the
management plugins, a nickname string for repl-chat. The provider document
lists, per provider, the concrete models it serves (the model-to-endpoint
mapping), and the entry wires model-manager with a credential
resolver that reads the `.core/` auth store or the host environment. Under
`cmd/core-agent` all payloads are resolved from `.core/` at startup (see
Configuration), and the entry may still override knobs with flags. A provider
marked `mock` needs no endpoint: `-mock` swaps the provider document for a
single in-process mock serving the models the default views resolve to.

## TUI Link

The TUI surface (charter `tui.pseudo`) reaches the core over one stdio
link: Neovim spawns `core-agent` in link mode and speaks JSON lines. The
link is a terminal like the repl — a line one way, events the other — and
the core never learns which surface is asking. The screen owns the chats'
lifecycle and names; the core owns the turns and the record.

One JSON object per line, UTF-8, newline-terminated. The codec and its
round-trip tests live in `internal/link`. The entry serves the link with
`-tui`: it composes the same seven plugins, reads requests from stdin, and
writes events to stdout, with component logs going to stderr so stdout carries
link lines only. One core serves one launch — it is a child of the screen
and dies with it. The screen points at the binary through `vim.g.nvchat_core`
(extra flags in `vim.g.nvchat_core_args`).

**Screen → core**

| Object | Meaning |
|---|---|
| `{"kind":"deliver","chat":"<id>","text":"..."}` | Run one agent turn on that chat; an unknown id starts a conversation. |
| `{"kind":"load","chat":"<id>"}` | Answer with that chat's recorded turns, in append order. |
| `{"kind":"jobs"}` | Answer with the launch's jobs in creation order, each naming its parent for the tree. |
| `{"kind":"peek","job":"<id>"}` | Answer with one job's state, last tick, and output so far; an unknown id answers an error. |

**Core → screen**

| Object | Meaning |
|---|---|
| `{"kind":"message","chat":"<id>","role":"assistant","text":"..."}` | One `chat.message`, attributed to its chat. |
| `{"kind":"loaded","chat":"<id>","messages":[{"role":"user","text":"..."}]}` | The answer to `load`; an unknown chat answers with an empty list. |
| `{"kind":"jobs","jobs":[{"job":"<id>","tool":"...","state":"...","parent":"<id>","age_ms":N,"idle_ms":N,"last_tick":"..."}]}` | The answer to `jobs`: the launch's jobs in creation order, each linked to its parent. |
| `{"kind":"peek","job":"<id>","tool":"...","state":"...","parent":"<id>","age_ms":N,"idle_ms":N,"last_tick":"...","output":"...","result":...,"error":"..."}` | The answer to `peek`: one job's state, last tick, and output so far, with its result once terminal. |
| `{"kind":"job","event":"started\|completed\|failed\|killed","job":"<id>","tool":"...","detail":"..."}` | A job state transition; detail carries tool args brief on start, formatted and truncated result on completion; ticks never cross the link. |
| `{"kind":"error","error":"..."}` | A malformed request or a failed turn; the loop continues. |

The job queries are read-only: the screen observes and peeks through them.
Start, peep, kill, and the tick cadence stay with the tool manager, and a
tick never becomes a link line or a transcript entry.

## Configuration

core-agent reads its configuration from `.core/` before it composes anything:
a user scope (default `~/.core`, relocated by `CORE_DIR`) and an optional
project scope (`./.core`), each carrying the same four files:

| File | Responsibility |
|---|---|
| `settings.json` | entry knobs: nick, conversation, context budget, agent model view, clock period |
| `providers.json` | the provider document (`{name, endpoint, credential, models}`) |
| `models.json` | the model views (alias, fallback, discuss) |
| `auth.json` | credentials by name; user scope only, mode 0600 |

**Resolution.** Every file is optional. The layers apply per key, highest
last: embedded defaults, the user files, the project files, the environment
(`CORE_NICK`, `CORE_MODEL`, `CORE_CONTEXT_BUDGET`), then the entry's flags
(`-nick` last). An absent file means "use the layer below". Named entries in
`providers.json` and `models.json` merge by name: an overlay replaces a
same-named entry and appends new ones. A project `auth.json` is ignored:
secrets do not live in repositories. A missing credential surfaces at the
first request that needs it, naming the provider.

**Validation.** A known file must parse as JSON. A malformed file fails the
load with an error naming that file, before any plugin activates. Unknown
files in `.core/` and unknown keys in a known file are ignored, so the
directory stays open to growth.

**First run.** When the user directory does not exist, the entry seeds
`settings.json`, `providers.json`, and `models.json` from the embedded
defaults and creates `auth.json` empty with mode 0600. Seeding never
overwrites a file that exists.

**Payloads.** The merged document splits into the entries' payloads: the
provider document to provider-manager, the model views to model-manager, the
conversation to chat-history, the budget to context-manager, the merged agent
knob to agent, and the nick to repl-chat. `cmd/core-agent -mock` swaps the
provider document for a single in-process mock serving the models the default
views resolve to.

**Credentials.** A provider's `credential` field holds a reference, never a
secret: `env:NAME` reads only the host environment, and `auth:NAME` reads the
named credential from `auth.json`, shadowed by a same-named environment
variable when one is set. The host resolves the reference when it substitutes
request headers, so a provider document carries only the reference and a
reference crosses, never a secret.

## Conformance

The Gherkin scenarios in [`features/`](features/) and
`plugins/*/specs/features/` are normative and run on Godog:

```console
$ go test ./...
```

`conformance/` holds the runner and the step definitions. Plugin scenarios
execute the host-testable libraries; system scenarios compose the seven plugins
at the host level with the same declarations, transcript, and pipeline (fast
and deterministic). The wasm ABI path is not exercised by the suite (review
decision); per-package Go tests cover each library and component beside
its code.

`internal/link`'s codec tests and `cmd/core-agent`'s link tests cover the
TUI link on the Go side; `tui/tests/run.sh` drives the screen's store
headlessly against a real link.

## Non-Functional Requirements

- **Determinism.** Given the same tree and transcript, the log transcript
  contains the same lines; tests assert content, not timing.
- **Isolation.** A composition plugin's library never imports another
  composition plugin's library; shared behavior crosses through kernel keys
  and payloads. Host-side components compose them explicitly, since the host
  owns the wiring.
- **Reclamation.** After unloading the tree, no fiber remains.
- **Portability.** The host builds with Go 1.23+; no cgo. The frozen guests
  still build with the wasip1 port via `cmd/build-plugins.sh`.

## Non-Goals

Extras beyond the seven plugins and the agent layer; local patches to memento
(changes go upstream); loading or unloading plugins from inside the REPL;
discussion merge orchestration; persistence of jobs, history, or credentials;
cross-process or out-of-tree composition; any layout beyond `plugins/`,
`cmd/`, and the `conformance/` runner in the first cut.

## Decisions

- **D1 — Kernel as-is.** memento is imported, not vendored or patched; missing
  capabilities become upstream proposals, tracked in the plugin specs that
  need them.
- **D2 — Plugin form: native components by default.** The seven starter plugins
  are compiled memento components with their host-testable logic in the plugin
  library; native-to-native calls use typed Go contracts over a shared
  host-side key set. The wasip1 guests stay committed as the frozen
  dynamic-extension route — neither built nor exercised by the suite.
  (Charter open question 1, settled.)
- **D3 — Spec placement.** System spec here; plugin-local specs under
  `plugins/<name>/specs/`, as memento keeps root specs plugin-independent.
  (Charter open question 2.)
- **D4 — LLM in the REPL.** The LLM drives the same REPL a human drives. The
  REPL does not load or unload plugins in the first cut: composition is the
  loader's job, exercised from `cmd/`; repl-chat's spec records this decision.
  (Charter open question 3.)
- **D5 — Shared key set.** All seven components register against one shared
  host-side key set (`internal/keys`), so injection satisfaction and provider
  identity work across plugins; each component binds its typed contracts, so
  the registration is the component's own tracked effect.
- **D6 — Transport landed upstream, not patched.** The host ABI now exposes
  host-mediated HTTP (`memento.http_request` with `http_response_len`/
  `http_response`), host-owned egress policy, and host-substituted credential
  references; memento's loader specifies and implements it. core-agent's native model-manager folds the transport role in: it performs
  the provider exchange over plain HTTP with host-side credential
  substitution, so a chat turn returns the provider's answer instead of a
  deterministic stub.
- **D7 — Godog conformance.** Every feature file runs on Godog from
  `conformance/` (the layout exception this plan proposed); plugin features
  bind to the libraries and native components, system features to the
  host-level composition; the wasm ABI path is not exercised by the suite
  (review decision), and per-package suites cover each component beside
  its code.
- **D8 — Data flow runs over typed contracts in-process.** The shared key set
  is the call surface: the agent's turn pipeline reaches chat-history,
  context-manager, and model-manager through their typed contracts — the same
  operation documents the handlers serve — and the terminal reaches the agent
  the same way, so the system composes live data, not only lifecycle. Wasm
  guests composed as extensions still use the `invoke` ABI.
- **D9 — Committed artifacts, frozen.** Each plugin's `.wasm` stays committed
  beside the plugin, but the suite neither builds nor exercises it;
  `cmd/build-plugins.sh` remains for the extension route, and the default
  composition does not instantiate it.
- **D10 — Payloads.** JSON for configurable plugins, string for the REPL
  nickname; defaults live in `cmd/core-agent` and are overridable by entries.
- **D11 — Provide binding is the component's own effect.** Each native component
  binds its declared provides during activation through `runtime.Bind`, each
  guest through `memento.bind` upstream (memento `plugins/wasm`); the temporary
  host-side binding adapter was removed. Nothing about registration is
  host-side anymore.
- **D12 — Providers declare the models they serve.** A provider's `models`
  list is the model-to-endpoint mapping, so a resolved model reaches a
  concrete endpoint without inventing naming heuristics. A concrete model no
  provider lists is a dangling reference and the call fails loudly.
- **D13 — The LLM is mocked in-process, by config.** A provider marked `mock`
  answers through model-manager's `MockCaller` instead of an exchange; no
  server and no network. Host-level conformance composes that same mock
  caller, so the deterministic transcript and the mockable system are one
  mechanism.
- **D14 — Host-side components live beside guest plugins.** The tool
  manager and the notification bus are Go packages under `plugins/<name>/`
  with their own specs, constructed in `cmd/core-agent` and wired into the
  native components — the manager as the agent's job service, the bus behind
  the publish path; they own OS resources and concurrency a serialized guest
  cannot. The subagent runner is host-side too: it registers as a manager
  tool over the agent loop. Extension guests reach the host side only through
  the ABI imports, never around. Subscriptions are host-configured — the
  assembler decides which component hears which topic — and waking a
  subscriber invokes its handler.
- **D15 — The terminal's loop is host-driven.** The entry runs the repl-chat
  session loop and wakes the terminal once per `chat.message`; the
  terminal runs one agent turn per line and renders the wake's events. A loop
  hosted in a serialized guest would hold its module lock for the whole session,
  so no wake could reach it (memento D14: a wake is a call like any other). The
  terminal is therefore a view: the transcript stays chat-history's, and the
  terminal's answer to a line carries no text.
- **D16 — The agent layer is jobs, events, and one loop.** A call is a job,
  always (tool-manager); components hear each other over a queued bus
  (notifications); the agent is a native component whose turn runs from wake to
  quiescence, yields at most one `chat.message`, and ends on a tool call.
  The loop is addressable — the terminal, the host waker, and a subagent's
  runner reach it the same way — and one implementation serves the top-level
  agent and every subagent. A subagent call is a manager job whose runner
  drives the child's turns; the child's conversation and context are its own,
  its speech stays private, and visibility and kill are subtree-only.
- **D17 — Configuration lives in `.core/`.** The entry reads a user scope
  (default `~/.core`, `CORE_DIR` relocates) and a project scope (`./.core`)
  of the same four files, layered per key over embedded defaults
  (defaults < user < project < env < flags). `auth.json` is user-scope only
  and credentials stay host-side: a reference (`env:NAME`, `auth:NAME`)
  crosses to the provider exchange, never a secret. First run seeds the defaults and never
  overwrites. (Charter `config.pseudo`, confirmed open questions 1–2.)
- **D18 — The TUI link is JSON lines on stdio.** Neovim spawns the entry in
  link mode and speaks one JSON object per line: `deliver` and `load` in;
  `message`, `loaded`, `job`, and `error` out (schema in TUI Link). JSON
  lines is the charter's wire-encoding open elected for the MVP — the least
  carrier for a child process that already has a voice. (`tui.pseudo`.)
- **D19 — The notification clock batches wakes, it never hides.** The
  agent's job wakes subscribe through the clock (`clock.period_ms`, default
  500): queued events flush together on a boundary, an empty boundary wakes
  no one, and events that arrive during a wake wait for the next boundary.
  Transitions still stream immediately to an observing surface, and the
  clock suppresses nothing the agent experiences — a batched tick wake is a
  normal turn, so the model may speak and its speech crosses as any other
  message. (#16, exhibitions principle.)
