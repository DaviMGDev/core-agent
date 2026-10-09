# AGENTS.md — nvchat (Neovim chat sandbox)

A chat interface explored inside Neovim: a session tree, a message list with senders, and a composer that is a real editor buffer. Launched via its own `nvchat` wrapper (`NVIM_APPNAME=nvchat`), in the same style as `mvim`. The repository is in the **design phase**: there is no plugin code yet, and this file is mandatory reading before any implementation starts.

## Mandatory reading order

Read these before changing anything:

1. `specs/SPEC.md` — the specification; the `sections:` list in its frontmatter is its contract.
2. `idea.md` — working notes, goals, and open questions.
3. `chat.layout.txt` — the screen structure (LAYOUT v1).
4. `chat.pseudo` — behavior and flow.
5. `chat.wireframe.svg` and `chat.wireframe.sessions.svg` — the visual wireframes (`chat.wireframe*.png` and `chat.wireframe*.ascii.txt` are renders).
6. `specs/features/*.feature` — the behavior conformance suite.

## Key commands

- **Render the wireframe (PNG)**: `./tools/svg2png.sh chat.wireframe.svg`
- **Render the wireframe (text)**: `./tools/svg2ascii.py chat.wireframe.svg chat.wireframe.ascii.txt`
- **Validate the layout**: `python3 ~/.pi/agent/skills/layout-txt/scripts/check_layout.py chat.layout.txt`
- **Launch**: `bin/nvchat` → `NVIM_APPNAME=nvchat exec nvim "$@"`, installed to `~/.local/bin` by `./install.sh` (mirror of `mvim`)
- **Launch from the repo**: `NVIM_APPNAME=nvchat nvim`, config in `~/.config/nvchat`
- **Dependencies**: `init.lua` installs neo-tree v3 + `plenary` + `nui` with `vim.pack` (Neovim 0.12+); `nvim-pack-lock.json` pins the revisions.
- **Test — always `shell-use`, functional or visual**: set the size explicitly (`shell-use run bin/nvchat --cols N --rows M`, or `shell-use resize N M`) — never test at the default 80×30. Test at the 160×50 grid (this 1280×800 display) and at the standard grids 80×24, 120×40, 160×50. Drive keys and assert on the rendered screen (`text`, `expect`, `get cursor`); for visual checks, `shell-use screenshot out.svg`, convert to PNG (`rsvg-convert -w 1280 -h 800` for the display check), and read the image back.

## Project structure

```
.
├── AGENTS.md                # This file
├── idea.md                  # Working notes: what and why
├── chat.layout.txt          # Screen structure — LAYOUT v1, source of truth for structure
├── chat.pseudo              # Behavior and flow — algorithmic pseudocode
├── chat.wireframe.svg       # Launch wireframe on a 160×50 terminal grid
├── chat.wireframe.png       # Render — generated, do not hand-edit
├── chat.wireframe.ascii.txt # Text render — generated, do not hand-edit
├── chat.wireframe.sessions.svg       # Workspace wireframe (sessions overlay open)
├── chat.wireframe.sessions.png       # Render — generated, do not hand-edit
├── chat.wireframe.sessions.ascii.txt # Text render — generated, do not hand-edit
├── init.lua                 # Config entry point: leader, vim.pack deps, setup
├── nvim-pack-lock.json      # Pinned plugin revisions (vim.pack)
├── bin/nvchat               # The only entry point (NVIM_APPNAME wrapper)
├── install.sh               # Installs bin/nvchat to ~/.local/bin
├── lua/nvchat/              # state, store, ui, files, sessions, messages, composer, draft, render
├── specs/
│   ├── SPEC.md              # The specification (sections declared in frontmatter)
│   ├── index.md             # specs/ registry and reading order
│   ├── log.md               # Spec change log — update on every spec change
│   └── features/            # Gherkin conformance suite
└── tools/
    ├── svg2png.sh           # SVG → PNG (rsvg-convert)
    └── svg2ascii.py         # SVG → text (grid-aware, 8×16 cells)
```

## Mandatory rules

### Source of truth

- The artifacts above are the source of truth. Do not implement behavior that none of them declares. If a new decision is needed, record it in `specs/SPEC.md` (Decisions) and, for flow, in `chat.pseudo` — before or in the same change as the code.
- When artifacts and implementation conflict, the artifacts win until deliberately updated. Update the artifact first, then the code.
- Keep artifacts in sync with each other: a structure change touches `chat.layout.txt`, and the wireframe follows.

### Structure and behavior

- The screen is exactly the one declared in `chat.layout.txt`: files panel (`sidebar`), main (`editor`, `chat`), sessions overlay (`sessions`), and `statusline`. Do not add screens, regions, or widgets the layout does not declare.
- Follow `chat.pseudo` for flows: start, toggle sidebar, choose a node, open a session, render messages, select a message, scroll, send, receive. Honor every `// decision:` marker.
- Selection is block-level; the tree keeps its expansion and cursor across toggles; a new message never steals a view that has scrolled away.
- The composer is a real Neovim buffer. Never fake an input widget.
- Prefer plain buffer text for rendering; use extmarks or virtual text only when a concrete need forces them, and record that decision.
- Message content is dynamic: never hard-code session names, messages, or counts beyond the samples in the artifacts.

### Launch and isolation

- All launches go through the `nvchat` wrapper: `NVIM_APPNAME=nvchat exec nvim "$@"`, config in `~/.config/nvchat`. Create `bin/nvchat` and `install.sh` mirroring `mvim`'s layout.
- Never read or write `~/.config/nvim` or `~/.config/mvim`, and never require the user's personal config.
- Plugins install under the `nvchat` appname through `vim.pack` (`stdpath("data")/site`); the pinned revisions live in `nvim-pack-lock.json`, committed to the repo.
- Unsent drafts may persist under the `nvchat` appname's own state directory (`stdpath("state")`); draft state goes nowhere else.

### Design

- Terminal-native only. No web idioms — no rounded corners, no web-style decoration. "Pretty" comes from cells, glyphs, highlights, and restraint.
- The wireframe's palette is not the UI's palette. Real colors and spacing wait for a future `DESIGN.md`; do not cement the wireframe colors into the UI.
- No backend is required. A mock or local store is fine; the store interface is list / load / deliver, with the backend left swappable until decided.

### Specs and conformance

- `specs/SPEC.md` declares its own contract: edit the frontmatter `sections:` and the `##` headings together. No undeclared `##` headings.
- Register new spec files in `specs/index.md` and log every spec change in `specs/log.md` (date + change + reason).
- Gherkin files begin with `Feature:` — no YAML frontmatter, no extra headers. Behaviors are expressed only as scenarios.
- When behavior changes, update `specs/features/*.feature` in the same change. The suite is the behavior contract even before it has a runner.

### Verification — definition of done

A change is not done until:

- [ ] `chat.layout.txt` still parses (run the validation command above).
- [ ] The wireframe renders (`chat.wireframe*.png`, `chat.wireframe*.ascii.txt`) are regenerated if either SVG changed.
- [ ] Functional and visual checks both go through `shell-use` at explicit sizes — the 160×50 grid (this 1280×800 display) and the standard grids 80×24, 120×40, 160×50 — never at the default PTY size: `nvchat` runs in a headless PTY, behavior is asserted on the rendered screen, and the `screenshot` image is read back (converted to 1280×800 for the display check).
- [ ] `specs/SPEC.md`, `specs/features/`, `chat.layout.txt`, and `chat.pseudo` reflect the change.
- [ ] `specs/log.md` has an entry for every spec change.

## Boundaries

### ✅ Always

- Read the artifacts before touching code.
- Keep structure, behavior, spec, and features consistent.
- Run the layout check and regenerate renders after edits.
- Test through `shell-use` — every functional or visual check.
- Keep the `nvchat` wrapper the only entry point.

### ⚠️ Ask first

- Adding a screen, region, widget, or spec section.
- Changing a recorded decision (supersede it, never silently rewrite it).
- Adding dependencies beyond Neovim + Lua.
- Deleting or renaming any artifact.

### 🚫 Never

- Treat the TUI like a web page.
- Hand-edit generated files (`chat.wireframe.png`, `chat.wireframe.ascii.txt`).
- Touch `~/.config/nvim` or `~/.config/mvim`.
- Invent UI content (sessions, messages, buttons) that the artifacts do not declare.
- Add YAML frontmatter or extra headers to `.feature` files.
