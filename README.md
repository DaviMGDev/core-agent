# core-agent

An agent core on [memento](https://github.com/DaviMGDev/memento): the kernel is imported as-is, every other capability is a plugin composed at runtime.

The current cut is deliberately small — five starter plugins, compiled as WebAssembly guests and driven through a REPL by the LLM like any other user. The **agent layer** on top of it (jobs, notifications, agents, subagents) is designed and tracked in [#7](https://github.com/DaviMGDev/core-agent/issues/7).

## How it works

core-agent follows memento's spatiotemporal composability: plugins declare the context keys they **inject** and **provide**, the kernel activates a plugin only when its dependencies are satisfied, deactivates dependents first when a provider withdraws, and reclaims every effect a plugin installed — no hand-written teardown. A plugin can therefore arrive at runtime, not only at build time.

Every plugin is one directory:

- `plugins/<name>/` — the host-testable Go library and its tests
- `plugins/<name>/guest/` — the wasip1 `package main` compiled to `.wasm`
- `plugins/<name>/specs/` — the plugin's own contract (Gherkin features included)

A chat turn flows through the composed plugins over the loader's `invoke` ABI:

```
user line → repl-chat → chat-history.append
                      → chat-history.recent
                      → context-manager.project
                      → model-manager.resolve → provider-manager → HTTP
                      → chat-history.append (assistant)
           → response line
```

## Starter plugins

| Plugin | Injects | Provides | Role |
|---|---|---|---|
| `repl-chat` | `chat-history`, `model-registry`, `llm-context` | `repl` | REPL session: prompts, commands, one response per turn |
| `provider-manager` | — | `provider-registry` | providers: name, endpoint, credential reference, served models |
| `model-manager` | `provider-registry` | `model-registry` | model views (alias, fallback chain, discussion group), cycles refused |
| `chat-history` | — | `chat-history` | conversation record: append and recent turns, keyed by conversation id |
| `context-manager` | `chat-history` | `llm-context` | projects a conversation into a bounded context window |

## Run

```console
$ go run ./cmd/core-agent -mock    # in-process mock LLM: no provider, no socket
$ go run ./cmd/core-agent          # uses the default provider document
```

Flags: `-mock` (in-process mock LLM), `-nick <name>` (the nickname the REPL announces). A real session needs a reachable provider; credentials cross as `env:VAR` references the host substitutes, so no secret enters guest memory.

Commands: `:help`, `:quit` (aliases `:q`, `:exit`); blank lines just re-prompt.

## Test and build

```console
$ go test ./...            # libraries, Godog conformance, end-to-end session
$ ./cmd/build-plugins.sh   # rebuild the .wasm guests beside each plugin
```

The Gherkin scenarios in `specs/features/` and `plugins/*/specs/features/` are normative and run on Godog from `conformance/`. The `.wasm` artifacts are committed beside their plugins, so tests and the entry need no rebuild step.

## Layout

```
specs/                 system specification + features
plugins/<name>/        library, tests, guest, package-local specs
cmd/core-agent/        entry: composes the five plugins and hosts the REPL
cmd/build-plugins.sh   rebuilds the guests
conformance/           Godog runner and step definitions
```

## Design

The system contract lives in [`specs/SPEC.md`](specs/SPEC.md); plugin contracts live beside their plugins. Specs precede source: a behavior without a spec is a behavior that does not get implemented.

The planned agent layer — every tool call a job, notifications as senses, an addressable agent loop, subagents as tools — is summarized in the epic [#7](https://github.com/DaviMGDev/core-agent/issues/7), with one issue per piece.

## License

[MIT](LICENSE)
