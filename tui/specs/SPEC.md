---
type: spec
title: "nvchat — Specification"
description: "A chat workspace explored inside Neovim: files panel, editor, chat, sessions overlay."
tags: [spec]
sections: [context, users, user-stories, tui-surface, architecture, decisions, stack, nfr]
created: "2026-10-08"
updated: "2026-10-09"
---

# nvchat — Specification

*This spec declares its own contract: every `##` section below appears in
`sections:` in the frontmatter, and nothing else is a `##` heading. `context`
and `decisions` are the fixed floor; the rest of the set is this project's
choice.*

## Context

nvchat is a chat workspace built on top of Neovim: an exploration of what
Neovim can host — windows, buffers, floats, plugins, extmarks, and
terminal-native design. It is a sandbox, not a supported client.

Goals:

- A files panel and normal editor windows around the chat — the sandbox
  should also be usable as an everyday editor.
- A chat browser — the launch's chats in a flat floating list, quick to
  summon instead of owning a permanent column.
- A message list that shows messages with their senders and stays selectable
  and scrollable.
- A composer that is a normal editor buffer, edited with real Neovim motions.
- A terminal-native look — no web idioms; "pretty" comes from cells, glyphs,
  highlights, and restraint.

Non-goals:

- No stability or compatibility promises.
- No web-style visual language.
- The core is required once the screen is wired: the store spawns it, and
  without it the screen freezes and says so.

Stakeholders: the author, as the sole writer and implementer. The working
notes live in [idea.md](../idea.md).

### Open questions

Not decisions yet; listed here so they do not hide.

- What happens when a send fails (the core refuses the turn).
- Whether the sessions overlay grows type-to-filter (v1 is j/k + enter).
- How editor windows and the chat column share the main area in richer
  layouts (more than one file open).
- Whether the keybindings become configurable; the v1 defaults are minimal.

## Users

One persona: **the writer**. The author exploring Neovim, who wants to read
and write chat messages without leaving the editor or learning a new UI.

Journey: launch `nvchat` → land in the most recent session → browse files
in the panel or summon the sessions overlay → open a session or a file →
read and select messages → type in the composer → send → see the reply
arrive without losing the reading position.

## User Stories

The Gherkin suite under `features/` is the executable form of these stories:
[startup.feature](features/startup.feature),
[files-panel.feature](features/files-panel.feature),
[sessions-overlay.feature](features/sessions-overlay.feature),
[jobs-overlay.feature](features/jobs-overlay.feature),
[message-list.feature](features/message-list.feature),
[composer.feature](features/composer.feature),
[navigation.feature](features/navigation.feature).

Story: US-001 — Browse chats from an overlay

As the writer,
I want the launch's chats in an overlay I can summon,
so that switching conversations never costs a permanent column.

Acceptance criteria (EARS):

- The system SHALL show the launch's chats flat in a floating overlay.
- The system SHALL move the keyboard focus between the files panel, the
  message list, and the composer without a mouse.
- WHEN the writer toggles the sessions overlay THE system SHALL show or hide
  it.
- WHEN the writer chooses New chat THE system SHALL create a blank chat and
  open it.
- WHILE the sessions overlay is hidden THE system SHALL keep its cursor over
  the same chat.
- WHEN the writer chooses a session THE system SHALL open it and close the
  overlay.

Story: US-002 — Read a conversation

As the writer,
I want a scrollable message list with senders and whole-message selection,
so that I can read a conversation and copy any message — or a run of
messages — in full.

Acceptance criteria (EARS):

- The system SHALL render each message as a block with its sender row and
  body.
- WHEN the writer moves the cursor to a message THE system SHALL select the
  whole block.
- WHEN the writer yanks a visual selection THE system SHALL copy every whole
  message block it covers, in order.
- WHEN a reply arrives THE system SHALL append it to the message list.
- WHEN a job changes state THE system SHALL append a system entry to the
  message list.
- IF the writer has scrolled away from the newest message THEN THE system
  SHALL keep the view where the writer left it.

Story: US-003 — Write and send

As the writer,
I want the draft in a normal Neovim buffer,
so that editing a message feels like editing any other text.

Acceptance criteria (EARS):

- The system SHALL keep the draft in a normal editor buffer.
- WHEN the writer presses Enter in the composer THE system SHALL send the
  draft.
- WHEN the writer inserts a line break in the composer THE system SHALL keep
  editing the draft instead of sending it.
- WHEN the writer sends a non-empty draft THE system SHALL append a message
  from the writer and clear the draft.
- IF the draft is empty THEN THE system SHALL NOT change the message list.
- The system SHALL NOT let an unsent draft block quitting.
- WHEN the writer quits with an unsent draft THE system SHALL save it for the
  open session.
- WHEN a session with a saved draft is opened THE system SHALL restore the
  draft into the composer.

Story: US-004 — Run the sandbox in isolation

As the writer,
I want to launch nvchat through its own wrapper,
so that the sandbox never touches my normal Neovim configuration.

Acceptance criteria (EARS):

- WHERE the `nvchat` wrapper is used THE system SHALL load the configuration
  under the `nvchat` appname.
- The system SHALL install plugins and keep runtime state under the `nvchat`
  appname's own directories.
- The system SHALL NOT read or modify the configurations of `nvim` or `mvim`.

Story: US-005 — Use nvchat as an editor

As the writer,
I want a files panel and real editor windows,
so that nvchat is also where I work on files.

Acceptance criteria (EARS):

- The system SHALL show the filesystem in a tree panel with the writer's
  files and directories.
- WHEN the writer chooses a file in the panel THE system SHALL open it in a
  normal editor window.
- The system SHALL place the chat in the main area: full width while no
  editor window is open, a right column while one is.
- WHEN the writer hides the chat THE system SHALL give the editor the full
  main area.

Story: US-006 — See the launch's jobs at a glance

As the writer,
I want the launch's jobs in an overlay I can summon,
so that I can see what is running and peek at a job's output without
leaving the chat.

Acceptance criteria (EARS):

- The system SHALL show the launch's jobs as a tree in a floating overlay.
- WHEN the writer toggles the jobs overlay THE system SHALL show or hide it.
- WHEN the writer selects a job THE system SHALL show its state, last tick,
  and output so far.
- WHEN a job changes state while the overlay is open THE system SHALL
  refresh the tree.
- The system SHALL NOT start, kill, or otherwise control a job from the
  overlay.
- The system SHALL NOT append ticks to the transcript.
- WHILE the jobs overlay is hidden THE system SHALL keep its cursor over the
  same job.

## TUI Surface

One screen, `chat`. Its structure is declared in
[chat.layout.txt](../chat.layout.txt) (LAYOUT v1); the visual wireframes are
[chat.wireframe.svg](../chat.wireframe.svg) (launch: files panel + full-main
chat), [chat.wireframe.sessions.svg](../chat.wireframe.sessions.svg)
(workspace: editor + chat column, sessions overlay open), and
[chat.wireframe.jobs.svg](../chat.wireframe.jobs.svg) (workspace: jobs
overlay open), each rendered to PNG and, as text, `.ascii.txt` next to it
(D22).

| Region | Type | Role |
| --- | --- | --- |
| Files panel | `sidebar:open` | neo-tree over the filesystem; toggles, opens files |
| Editor | `editor:open` | normal Neovim windows for opened files |
| Chat | `chat:visible` | the message list and composer column |
| Message list | `message-list` | scrollable; whole-block selection, single or ranged |
| Composer | `composer` | a real buffer holding the draft |
| Sessions overlay | `sessions:closed` | floating browser for the launch's chats |
| Jobs overlay | `jobs:closed` | read-only floating view of the launch's jobs: the manager's tree with a peek preview |
| Statusline | `statusline` | editor state (mode) and open session summary |

Rendering targets the 160×50 grid at 1280×800 (8×16 px cells) and is checked
at the standard grids 80×24, 120×40, and 160×50. The v1 default keys are a
minimal set for driving the features (D15, D19–D21); the wireframes show
none.

## Architecture

The model is a flat launch-local list of chats; each chat is one
conversation with the core and holds ordered messages (tui.pseudo). The
files panel is real neo-tree, a plugin window over the
filesystem; the message list, composer, jobs overlay, and sessions overlay
are backed by
nvchat buffers; the statusline is the editor's single global statusline,
not a buffer. The main area holds normal editor windows and the chat
column: the chat takes the full width while no editor window is open, and
becomes a right column while one is (D21).

Behavior is specified in [chat.pseudo](../chat.pseudo): start, toggle the
files panel, open a file, place and hide the chat, summon and choose from
the sessions overlay, summon and read the read-only jobs overlay, render
messages, select a message, scroll the message list, send, and receive a
reply. The recorded design decisions are in Decisions below.

The store is an abstraction — list the launch's chats, open the recent one,
load a chat's turns, deliver a sent line, and answer the read-only job
queries (`jobs`, `peek`). The core backs it over the TUI link; the UI calls
the same operations. Unsent drafts are not part of
the store: they are client-local state
under the `nvchat` appname's state directory, saved at exit and on session
switches, and restored when a session opens (D18).

## Decisions

- **D1 — Launch through the `nvchat` wrapper.** `bin/nvchat` runs
  `NVIM_APPNAME=nvchat exec nvim "$@"`, with `install.sh` copying it to
  `~/.local/bin`, mirroring `mvim`. This is the default entry point, not a
  test-only convenience. Reason: isolation from the author's `nvim`/`mvim`
  setups.
- **D2 — Design before build.** Wireframe → layout → pseudocode → plugins.
  The design artifacts (`idea.md`, `chat.layout.txt`, `chat.pseudo`,
  `chat.wireframe.*`) stay in the repository root by explicit direction; this
  `specs/` directory is the specification proper (recorded in
  [log.md](log.md)).
- **D3 — Selection is block-level.** A message is selected as a whole block,
  so a yank copies the sender row and body together.
- **D4 — The chat overlay keeps its place.** The cursor survives toggling
  the overlay.
- **D5 — New messages never steal the reading position.** A reply pins the
  view to the newest message only when the writer is already at the end.
- **D6 — Plain buffer text first.** Extmarks and virtual text are deferred
  until a rendering need forces them.
- **D7 — One screen; palette deferred.** The chat screen is the only screen
  for now, and the wireframe's palette is not the UI's palette — colors and
  spacing wait for a future `DESIGN.md`.
- **D8 — The wireframe's text view is grid-aware.**
  `tools/svg2ascii.py` reads the terminal-grid SVG geometry instead of
  rasterizing, because a raster-to-ASCII pass is unreadable at 13 px text.
- **D9 — The Gherkin suite is the behavior contract.** `features/` holds the
  scenarios; it is not wired to a runner yet because there is no
  implementation to run.
- **D10 — Highlights use extmarks; buffer text stays plain.** The title, the
  active session node, sender rows and times, and the selected message block
  are drawn with extmarks (`line_hl_group`/`hl_group`) over plain buffer
  text; no virtual text is used. Reason: block-level selection must be
  visible to the writer, and attribute-only decoration is the smallest need
  that forces extmarks (D6).
- **D11 — The sidebar is plain Neovim windows.** A real full-height
  vertical split with its own buffer and a hand-rolled tree; no neo-tree or
  other dependency beyond Neovim + Lua. Reason: keep the sandbox
  dependency-free.
- **D12 — The store is core-backed.** `list` / `recent` / `load` / `deliver`
  stay the interface; the store module spawns `core-agent -tui` per launch
  and speaks the JSON-lines link, so the UI never learns which backend owns
  the record.
- **D13 — One message-list buffer, re-rendered.** The list is a single
  buffer redrawn from the open session's messages — not one buffer per
  session.
- **D14 — Minimal default keys.** `<C-b>` toggles the sidebar (normal and
  insert mode); in the overlay `<CR>` chooses a chat (New chat creates one);
  in the composer,
  normal-mode `<CR>` sends while Enter stays a newline; in the message list
  `j`/`k` move (line-to-line inside a block, skipping separators between
  blocks) and `y`/`yy` yank the selected block. Window navigation is stock
  Neovim. Reason: every feature must be reachable from the keyboard;
  anything more waits for a real keybinding scheme.
- **D15 — Leader keys and Enter-to-send (supersedes D14's toggle and
  composer keys).** `<Space>` is the leader (`mod`). `<leader>e` toggles the
  chat overlay; `<leader>t`, `<leader>m`, and `<leader>c` put the keyboard on
  the chat overlay (opening it first if hidden), the message list, and the
  composer — every region is reachable without a mouse. `<C-b>` remains an
  alias for the overlay toggle so it also works from insert mode. In the
  composer `<CR>` sends from insert and normal mode; `<C-j>` — and `<C-CR>`
  in terminals that report it — inserts a line break instead of sending.
  Everything else in D14 (overlay `<CR>`, message-list `j`/`k`/`y`)
  stands. Reason: the writer could not move between the regions without the
  mouse, and the chat gesture writers expect is Enter to send — line breaks
  need an explicit modifier.
- **D16 — No chat header.** The `chat-header` region is removed from the
  screen: the open session is already marked in the chat overlay and named
  in the statusline summary, and the header icon had no behavior. The
  message list starts at the top of the main column. Reason: user direction
  — the chat needs no title of its own; the panel is where the title lives.
- **D17 — An empty draft is clean.** Typing in the composer and erasing back
  to empty leaves the buffer unmodified, so `:q`/`:qa` never hits E37/E162.
  A non-empty draft still carries the normal editor warning and needs
  `:qa!`. Reason: the reported trap — an erased draft blocked quitting; the
  draft is not a document, but unsent text keeps its safety net.
- **D18 — Unsent drafts survive quitting (supersedes D17's warning
  clause).** The composer never blocks `:q`/`:qa`: its buffer never counts as
  a modification. The draft of the open session is written at `VimLeavePre`
  and on session switches to the `nvchat` appname's state directory
  (`stdpath("state")/drafts.json`, keyed by session id), restored when that
  session opens, and cleared when the draft is sent. Reason: user direction
  — text left in the composer must be there on reopen, so the quit warning
  has nothing left to protect.
- **D19 — The files panel is real neo-tree (supersedes D11).** The sidebar
  is neo-tree v3 over the filesystem: a left, full-height split with its own
  window, toggled by `mod+e` (with `<C-b>` kept as the insert-mode alias) and
  focused by `mod+t`. `plenary.nvim` and `nui.nvim` are installed by
  `vim.pack` from `init.lua` under the `nvchat` appname;
  `nvim-web-devicons` is deliberately skipped. Neovim 0.12 becomes the
  version floor. Reason: user direction — the sandbox should also be a
  normal editor, and a hand-rolled filesystem tree is not worth writing.
- **D20 — Chats move to a floating overlay (supersedes D14/D15's tree
  bindings).** `mod+s` opens a floating picker over the workspace with
  cursor memory (D4); `j`/`k` move, `<CR>` chooses and closes, `<Esc>`
  closes, and a `New chat` row sits at the bottom. The rows are the launch's
  chats, flat — threads and groups are gone with `tui.pseudo` — and the
  store interface is unchanged. Reason: the files panel took the sidebar's
  column; on demand beats permanent width.
- **D21 — The chat is placed automatically and can be hidden.** The chat
  column (message list + composer) fills the main area while no editor
  window is open and becomes a right column while one is; closing the last
  editor window returns it to full width. `mod+h` hides and shows the chat,
  and `mod+m`/`mod+c` focus its regions; opening a session from the overlay
  shows the chat again. Reason: the chat stays present without owning the
  screen.
- **D22 — Three wireframes, one per state.** `chat.wireframe.svg` draws the
  launch state (files panel + full-main chat);
  `chat.wireframe.sessions.svg` draws the workspace (editor + chat column)
  with the sessions overlay open; `chat.wireframe.jobs.svg` draws the
  read-only jobs overlay with the job tree and a peek preview. One frame
  cannot carry every state honestly. Reason: the overlays and the two chat
  placements are runtime states; the layout file's Meta notes them.
- **D23 — The jobs overlay is read-only and tick-free.** `mod+j` opens a
  floating overlay of the launch's jobs as the manager's tree, with the
  selected job's peek (state, last tick, output so far); `j`/`k` move, `r`
  refreshes on demand, and job transitions refresh it while it is open. It
  never starts, kills, or controls a job — start, peep, kill, and the tick
  cadence stay with the tool manager — and ticks never become transcript
  entries. Reason: the writer sees what is running without the transcript
  losing its one narrative surface (system #15).
- **D24 — A visual selection yanks whole blocks (extends D3).** A visual
  selection in the message list snaps outward to whole message blocks: a
  yank copies every covered block in order — sender row and body, one blank
  line between messages — and leaves visual mode, so `ggVGy` copies the
  whole conversation. D3's block unit stands: no partial message is ever
  copied, and normal-mode `y`/`yy` keep the single selected block. Reason:
  user direction — the writer must be able to copy a whole conversation,
  not one message at a time.

## Stack

- **Editor:** Neovim 0.12 or newer (`vim.pack`), the floor set by the
  plugin install path (D19).
- **Plugin language:** Lua, living in this repository as the `nvchat`
  configuration — repo root `init.lua` plus `lua/nvchat/` modules
  (`state`, `store`, `ui`, `files`, `sessions`, `jobs`, `messages`,
  `composer`, `draft`, `render`). The repository is the config; neo-tree v3 with
  `plenary.nvim` and `nui.nvim` is installed by `vim.pack` under the
  `nvchat` appname (D1, D19).
- **Design formats:** LAYOUT v1 ([chat.layout.txt](../chat.layout.txt)),
  Pseudolanguage ([chat.pseudo](../chat.pseudo)), SVG
  ([chat.wireframe.svg](../chat.wireframe.svg),
  [chat.wireframe.sessions.svg](../chat.wireframe.sessions.svg),
  [chat.wireframe.jobs.svg](../chat.wireframe.jobs.svg)).
- **Tooling:** [tools/svg2png.sh](../tools/svg2png.sh) (rsvg-convert) and
  [tools/svg2ascii.py](../tools/svg2ascii.py) (grid-aware text render).
- **Testing:** terminal screenshots read as images, at 1280×800 and the
  standard grids; the Gherkin suite in `features/` is the behavior contract,
  not yet wired to a runner.

## NFR

- The system SHALL render the screen legibly at the 80×24, 120×40, and
  160×50 terminal grids.
- The system SHALL keep the region structure declared in
  [chat.layout.txt](../chat.layout.txt), with no region overflowing into
  another.
- WHERE the core is unreachable THE system SHALL freeze the transcript, say
  so in place, and refuse further sends.
