# core-agent

An agent core on [memento](https://github.com/DaviMGDev/memento): the kernel is imported as-is, every other capability a plugin composed at runtime.

The current cut composes **six plugins** as WebAssembly guests and adds the **agent layer** on top: a host-side tool manager where every call is a job, a queued notification bus, an addressable agent turn loop, a terminal that renders `chat.message`, and subagents callable as tools. The layer is tracked in the epic [#7](https://github.com/DaviMGDev/core-agent/issues/7).

## How it works

core-agent follows memento's spatiotemporal composability: plugins declare the context keys they **inject** and **provide**, the kernel activates a plugin only when its dependencies are satisfied, deactivates dependents first when a provider withdraws, and reclaims every effect a plugin installed — no hand-written teardown. A plugin can therefore arrive at runtime, not only at build time.

Every plugin is one directory:

- `plugins/<name>/` — the host-testable Go library and its tests
- `plugins/<name>/guest/` — the wasip1 `package main` compiled to `.wasm`
- `plugins/<name>/specs/` — the plugin's own contract (Gherkin features included)

Host-side components — the tool manager, the notification bus, and the subagent runner — follow the same shape but run in the host process; guests reach them only through the host ABI.

A chat turn flows through the composed plugins over the loader's `invoke` ABI:

```
user line → terminal (host-driven session) → agent-loop
              → chat-history.append
              → chat-history.recent
              → context-manager.project
              → model-manager.resolve → provider-manager → HTTP
              → chat-history.append (assistant)
              → publish chat.message
           → terminal renders the event
```

A tool call ends the turn: the agent starts a **job** (the manager owns the registry and the lifecycle), and the job's completion or tick wakes the agent through the bus — the LLM decides whether to speak. A **subagent** is a tool whose job drives a child agent loop with its own conversation, model, and budget; the reply comes back and the child's turns stay private. Killing a job is accepted at any point, honored at the next safe point, and kills its descendants.

### Native tool calling and presentation

Models call tools natively over OpenAI-compatible interfaces:

- **Presented surface:** The registry's callables (`name`, `description`, argument `parameters` schema) reach the top-level agent at composition and children via `childConfig`.
- **Presentation:** The agent controls what the model sees: configured tools only, hidden tools pruned, and renamed tools relabeled. Schemas travel intact through hiding and renaming. When a model calls an aliased tool label, the agent reverse-maps the name to the registry's name before starting the job.
- **Provider transport:** When the presented surface is non-empty, `model-manager` carries the OpenAI-compatible `tools` array in the request body (omitted when empty).
- **Normalization:** Provider responses carrying native `tool_calls` are normalized into the agent's `{"tool", "args"}` directive (the first call stands, further calls drop). The directive path remains identical for mock, scripted, and native providers alike.

## Starter plugins

| Plugin | Injects | Provides | Role |
|---|---|---|---|
| `provider-manager` | — | `provider-registry` | live provider registry: register, unregister, list, provider-for |
| `provider-openai` | `provider-registry` | — | provider plugin: registers OpenAI-compatible provider at activation, reverts on unload |
| `model-manager` | `provider-registry` | `model-registry` | model views (alias, fallback, discuss), resolves providers through the live registry |
| `chat-history` | — | `chat-history` | conversation record: append and recent turns, keyed by conversation id |
| `context-manager` | `chat-history` | `llm-context` | projects a conversation into a bounded context window |
| `agent` | `chat-history`, `model-registry`, `llm-context` | `agent-loop` | the turn loop: one `chat.message` per turn, jobs as tools, its own context |
| `repl-chat` | `agent-loop` | `repl` | the terminal: prompt, commands, renders `chat.message` wakes |

Host-side components: `tool-manager` (registry and jobs), `notifications` (the queued bus), and `subagent` (the manager tool that calls an agent).

## Run

```console
$ go run ./cmd/core-agent -mock    # in-process mock LLM: no provider, no socket
$ go run ./cmd/core-agent          # uses the default provider document
```

Flags: `-mock` (in-process mock LLM), `-nick <name>` (the nickname the REPL announces). A real session needs a reachable provider; credentials cross as references the host substitutes (`env:VAR` for the environment, `auth:NAME` for the `.core/` auth store), so no secret enters guest memory.

Commands: `:help`, `:quit` (aliases `:q`, `:exit`); blank lines just re-prompt. When the model calls a tool, the call runs as a job and its completion is reported back to the session unprompted.

## Configuration

The entry resolves its configuration from `.core/` before it composes anything — the way pi keeps its agent directory. The user scope is `~/.core`, relocated by the `CORE_DIR` environment variable; an optional project scope `./.core` layers over it. Each scope carries the same files:

| File | Responsibility |
|---|---|
| `settings.json` | entry knobs: nick, conversation, context budget, agent model view |
| `providers.json` | providers: name, endpoint, credential reference, served models |
| `models.json` | model views: alias, fallback chain, discussion group |
| `auth.json` | credentials by name, mode 0600 — user scope only |

Missing files fall back to the embedded defaults. Layers apply per key, highest last: defaults < user < project < environment (`CORE_DIR`, `CORE_NICK`, `CORE_MODEL`, `CORE_CONTEXT_BUDGET`) < flags (`-nick`). Named entries in `providers.json` and `models.json` merge by name: an entry from a higher layer replaces a same-named entry and new entries append. A malformed known file fails startup naming the file; unknown files and keys are ignored. The first run seeds the user scope from the defaults and an empty `auth.json`, never overwriting a file that exists.

The shipped default points the agent at ollama (`https://ollama.com/v1`) with `gemma4:cloud` behind the `fast` view. Put the key in `~/.core/auth.json`:

```json
{ "ollama": { "type": "api_key", "key": "..." } }
```

Credentials stay host-side: a provider references a credential (`auth:ollama`, or `env:VAR` for the environment), the guest payload carries only the reference, and the host substitutes the secret when it sends the request. A same-named environment variable shadows an `auth:` reference.

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
plugins/tool-manager/  host-side: the tool registry and job engine
plugins/notifications/ host-side: the queued event bus
plugins/subagent/      host-side: an agent callable as a tool
cmd/core-agent/        entry: composes the plugins and the agent layer, drives the session
cmd/build-plugins.sh   rebuilds the guests
conformance/           Godog runner and step definitions
```

## Design

The system contract lives in [`specs/SPEC.md`](specs/SPEC.md); component contracts live beside their components. Specs precede source: a behavior without a spec is a behavior that does not get implemented.

The charter behind the agent layer lives outside the repository; the epic [#7](https://github.com/DaviMGDev/core-agent/issues/7) is the public record, with one issue per piece.

## License

[MIT](LICENSE)
