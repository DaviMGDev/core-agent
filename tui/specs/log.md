---
type: log
title: "specs/ log"
description: "Activity log for the specs/ directory"
created: "2026-10-08"
updated: "2026-10-09"
---

# specs/ log

## 2026-10-09

- Added US-006 and D23 (D22 now carries a third wireframe): the read-only
  jobs overlay — `mod+j` opens a floating tree of the launch's jobs with
  the selected job's peek (state, last tick, output so far), refreshed by
  state transitions, never starting, killing, or controlling a job, and
  never painting ticks into the transcript. `jobs-overlay.feature` is
  added; the surface, architecture, and stack sections note the `jobs`
  module and the read-only `jobs`/`peek` store queries. Reason: user
  direction — the writer sees what is running without leaving the chat.

## 2026-10-08

- Initialized `specs/` — nvchat, status draft.
- Added the Gherkin behavior suite: `features/startup.feature`,
  `features/session-tree.feature`, `features/message-list.feature`,
  `features/composer.feature`.
- Placement decision: the design artifacts (`idea.md`, `chat.layout.txt`,
  `chat.pseudo`, `chat.wireframe.*`) stay in the repository root by explicit
  user direction; `specs/` links to them instead of owning copies.
- Added D10: highlights (title, active node, sender/time, selected block)
  use extmarks over plain buffer text; no virtual text. Reason: block
  selection must be visible; attribute-only decoration is the smallest need
  that forces extmarks (D6).
- Implementation landed (plan `nvchat-v1`): the repository is now the
  `nvchat` config — `init.lua`, `lua/nvchat/*` (`state`, `store`, `ui`,
  `tree`, `messages`, `composer`, `render`), `bin/nvchat`, `install.sh`.
- Resolved open questions and recorded D11–D14: plain-window sidebar (D11),
  in-memory mock store for v1 (D12), one re-rendered message-list buffer
  (D13), minimal default keys (D14). `chat.pseudo` gained the explicit
  expand/collapse action; the TUI Surface keybinding note points at D14.
  `idea.md` points at these decisions.
- Verified in an `nvchat` session: every scenario in `features/` holds
  (startup, session tree, message list, composer); rendering checked at
  1280×800 and the standard grids 80×24 / 120×40 / 160×50; the layout
  validator passes. No feature files changed.
- Added D15, superseding D14's toggle and composer keys: `mod` is `<Space>`;
  `<leader>e` toggles the session tree (`<C-b>` stays an insert-mode alias);
  `<leader>t`/`<leader>m`/`<leader>c` focus the tree, message list, and
  composer; the composer sends on `<CR>` in normal and insert mode, with
  `<C-j>`/`<C-CR>` inserting a line break. Reason: the writer could not move
  between the regions without the mouse, and expected Enter to send rather
  than break lines.
- Updated `chat.pseudo`: a focus-a-region flow, the mod+E decision on the
  sidebar toggle, and the Enter/line-break decision on send.
- Updated the behavior suite: `composer.feature` gains the Enter/line-break
  rule, and `features/navigation.feature` is added for keyboard region focus;
  registered in `index.md`.
- Implemented D15 and verified in `shell-use` at explicit sizes: functional
  checks at the 160×50 grid (this display) and at 80×24/120×40 (Enter send,
  ctrl+j line break, mod+E toggle, mod+T/M/C focus, hidden-tree focus, tree
  cursor preserved across toggles), and visual checks read back as images at
  1280×800 and 80×24/120×40/160×50. The layout validator passes.
- Removed the `chat-header` region (D16), on user direction: the session
  title already lives in the session tree and the statusline, and the header
  icon had no behavior. `chat.layout.txt` drops the block; the wireframe and
  its PNG/ASCII renders are regenerated; the message list starts at the top
  of the main column.
- Added D17: an empty draft is clean — typing in the composer and erasing
  back to empty no longer leaves the buffer modified, so `:q`/`:qa` does not
  warn (E37/E162); a non-empty draft still warns. `composer.feature` gains
  the rule and `chat.pseudo` records the erase decision.
- Added D18 (supersedes D17's warning clause): unsent drafts are client-local
  state — saved at `VimLeavePre` and on session switches under
  `stdpath("state")/drafts.json`, keyed by session, restored when a session
  opens, cleared on send; the composer never blocks `:q`/`:qa`. The
  architecture and stack note the `draft` module, `chat.pseudo` gains the
  quit flow, and `composer.feature` gains the quit/survival rules. Reason:
  user direction — unsent text must return on reopen.
- Redesigned for the workspace (D19–D22): the sidebar becomes real neo-tree
  over the filesystem (plenary + nui via `vim.pack`, Neovim 0.12 floor;
  supersedes D11), the session tree moves into a floating overlay (mod+s;
  supersedes D14/D15's tree bindings), and the chat is placed automatically
  (full main area without an editor window, right column with one) and hidden
  with mod+h. `chat.layout.txt` restructured, `chat.pseudo` rewritten for the
  new flows, US-001 reworked and US-005 added, TUI Surface and Stack updated,
  and a second wireframe recorded (D22, drawn in the next stage). Reason:
  user direction — nvchat should also be an everyday editor.
- Reworked the behavior suite for the workspace: `session-tree.feature`
  becomes `sessions-overlay.feature` (overlay toggle, memory, expand,
  choose), `files-panel.feature` is added (filesystem panel, editor windows,
  automatic chat placement, hiding the chat), and `navigation.feature`
  covers the new keys (mod+e files, mod+t focus files, mod+s overlay, mod+h
  chat). Registered in `index.md`.
- TUI integration landed (plan `tui-integration`): the store is core-backed
  — it spawns `core-agent -tui` per launch and speaks the JSON-lines link
  over stdio — the overlay lists the launch's chats flat (threads and
  groups gone), job state changes render as system entries beside you and
  the agent, and a dead link freezes the transcript, renders the failure in
  place, and makes the composer read-only. D12 updated to the core-backed
  store, the NFR gains the freeze clause, US-002 gains the system-entry
  criterion, `sessions-overlay.feature`/`message-list.feature`/
  `startup.feature`/`composer.feature` describe the flat model, and
  `chat.pseudo`, `idea.md`, `chat.layout.txt`, and the wireframe SVG/PNG/
  ASCII renders are reconciled with `tui.pseudo`. Reason: the accepted v2
  charter — one agent, one link, flat chats.
