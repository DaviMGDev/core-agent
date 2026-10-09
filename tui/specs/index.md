---
type: index
title: "specs/ — Index"
description: "Node registry and reading order for the specs/ directory"
created: "2026-10-08"
updated: "2026-10-08"
---

# specs/ — Index

Project status: draft

## Nodes

| File | Title | Description |
|------|-------|-------------|
| [SPEC.md](SPEC.md) | nvchat — Specification | The spec monolith: why, who, what, how |
| [log.md](log.md) | specs/ log | Activity log of spec changes |
| [startup.feature](features/startup.feature) | Startup | Launching lands in the most recent session |
| [files-panel.feature](features/files-panel.feature) | Files panel | Filesystem browsing, editor windows, chat placement |
| [sessions-overlay.feature](features/sessions-overlay.feature) | Sessions overlay | Toggling, expanding, and choosing sessions |
| [message-list.feature](features/message-list.feature) | Message list | Rendering, block selection, and the reading position |
| [composer.feature](features/composer.feature) | Composer | The draft buffer, Enter-to-send, and line breaks |
| [navigation.feature](features/navigation.feature) | Keyboard navigation | Region focus and the panel/overlay toggles |

## Reading order

SPEC.md — read top to bottom; the declared `sections:` in its frontmatter is
the contract. The Gherkin files under `features/` are the behavior conformance
suite backing the User Stories. Design artifacts live outside `specs/` at the
repository root by recorded placement decision (see `log.md`): `idea.md`,
`chat.layout.txt`, `chat.pseudo`, `chat.wireframe.*`.
