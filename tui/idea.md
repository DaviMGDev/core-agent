# nvchat

nvchat is a small chat workspace built on top of Neovim. It is not meant to
be a supported chat client — it is a sandbox for exploring what I can get out
of Neovim: windows, buffers, floats, plugins, extmarks, and whatever makes a
terminal feel good.

## What I want to explore

- **Files panel** — real neo-tree over the filesystem; the sandbox should
  also work as an everyday editor.
- **Sessions overlay** — the launch's chats summoned from the keyboard
  over the workspace: flat and quick, no permanent column.
- **Message view** — messages with their senders; selectable and scrollable.
- **Composer** — whatever text the writer is currently writing, as a normal
  editor buffer. Real Neovim motions, not a fake input box.
- **Terminal-native looks** — TUI design is not web design. There is no CSS in
  here; "pretty" has to come out of cells, glyphs, highlights, and restraint.

This list is probably incomplete on purpose — part of the exploration is
discovering what the UI actually needs once it is on screen.

## Shape of the screen

One screen (`chat.layout.txt` holds the structural spec):

1. **Files panel** — left, real neo-tree; opens files into normal editor
   windows.
2. **Main area** — editor windows and the chat column; the chat is full width
   while no file is open, a right column while one is, and hides with a key.
3. **Chat column** — message list above, composer below.
4. **Sessions overlay** — a floating browser summoned from the keyboard; the
   launch's chats, flat.

## Design before build

The idea is worked out as artifacts before any plugin code:

| Artifact | Captures | Status |
|---|---|---|
| `chat.wireframe.svg` | launch state: files panel + full-main chat | done |
| `chat.wireframe.sessions.svg` | workspace state: editor + chat column, overlay open | done |
| `chat.layout.txt` | screen structure (LAYOUT v1) | done |
| `chat.pseudo` | behavior and flow (algorithmic pseudocode) | done |

Each wireframe has companion renders — `.png` (visual) and `.ascii.txt`
(plain text) — regenerated with `tools/svg2png.sh` and `tools/svg2ascii.py`.
The two states are recorded in `specs/SPEC.md` (D22).

The point of this order is to settle *what* the screen is and *how it behaves*
before deciding *which plugin mechanism* to lean on — splits, floats,
extmarks, neo-tree internals.

## Running it — the `nvchat` wrapper

nvchat is opened by calling `nvchat`, never bare `nvim` — a dedicated wrapper
built the same way as my `mvim` (config repo cloned as its own appname,
`bin/mvim` installed into `~/.local/bin`):

- `bin/nvchat` — a script that runs `NVIM_APPNAME=nvchat exec nvim "$@"`.
- `install.sh` — copies it to `~/.local/bin`, same as mvim.
- `NVIM_APPNAME=nvchat` gives the sandbox its own config dir
  (`~/.config/nvchat`), fully separate from my real `nvim` and `mvim` setups.

This is the default entry point, not a test-only convenience: opening the chat
is `nvchat`. Tests just launch it the same way.

## Testing the rendering

Rendering is verified with `shell-use`: run `nvchat` in a headless PTY at an
explicit size, capture it, and read the image back (`screenshot` → SVG →
PNG). Check at least:

- this display: 1280×800 (the 160×50 grid)
- standard terminal grids: 80×24, 120×40, 160×50

## Open questions

The v1 build resolved its questions in `specs/SPEC.md` (D11–D18). The
workspace redesign moved the filesystem into the sidebar and the sessions
into an overlay (D19–D22). What remains open — send failures, empty store, a
real backend, overlay filtering, configurable keys — is tracked in the
spec's Open questions.

## Non-goals

- Not a supported client: no stability promises, no compatibility concerns.
- No web-style visual language.
- No plugin beyond the recorded set (neo-tree v3 with `plenary` and `nui`).
- The core is required once the screen is wired: nvchat spawns it, and
  without it the screen freezes and says so.
