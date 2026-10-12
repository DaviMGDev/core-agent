# Agent Development Guidelines & Gitman Flow

This repository enforces strict **Gitman Flow** standards for all automated agents and human contributors.

---

## 1. Commit Discipline: Atomic Mini-Commits Only

**Rule #1: NEVER create monolithic or omnibus commits.**

- **Atomic & Granular:** Every commit must represent exactly one logical, indivisible unit of work (e.g. adding a type definition, fixing a single bug, adding a specific test suite, or updating documentation).
- **Commit Early and Often:** Do not hoard changes across multiple deliverables or tasks. Commit immediately after completing and verifying each small task.
- **Conventional Commits:** All commit messages must follow the standard conventional commit format:
  ```text
  <type>(<scope>): <short imperative summary> (#<issue>)

  [optional body explaining motivation and consequences]
  ```
  - `feat`: A new feature or capability.
  - `fix`: A bug fix or wire protocol patch.
  - `test`: Adding or updating test cases.
  - `refactor`: Code reorganization without functional or behavioral changes.
  - `docs`: Documentation updates.
  - `chore`: Build scripts, dependencies, or tool configurations.

- **Green Main Invariant:** Every single commit must compile cleanly and pass existing automated tests (`go test ./...`). Never commit broken intermediate code.

---

## 2. Examples: Good vs. Bad Commits

### ❌ BAD (Omnibus / "Huge" Commits)
```text
# FORBIDDEN: bundling multiple issues and surfaces into one giant dump
git add .
git commit -m "fix: implement native tools, buffer sizes, json parsing and tests (#19, #30, #31, #39)"
```

### ✅ GOOD (Gitman Flow / Mini-Commits)
Break the work into small, focused, easily reviewable and bisectable commits:

```text
git commit -m "feat(keys): add tool role and tool_call_id to message structures (#30, #31)"
git commit -m "fix(agent): format tool execution results with native tool role (#30)"
git commit -m "fix(model-manager): serialize tool_call_id and tool messages for provider (#31)"
git commit -m "fix(model-manager): pass parallel_tool_calls false in provider request (#38)"
git commit -m "fix(agent): prevent in-band text parser from hijacking conversational json (#39)"
git commit -m "feat(guest): increase guest response buffer cap to 64KiB and add oversize error (#19)"
git commit -m "fix(link): guard callers against unmarshaling empty responses (#19)"
git commit -m "test(agent): add regression tests for large payloads and directive isolation (#19, #39)"
```

---

## 3. Working With Tasks & Plans

When executing plans or milestones:
1. **One Task = One Commit (or more):** Finish the work for the task, verify its checkable outcome, and commit it immediately before advancing to the next task.
2. **Review Diff Before Committing:** Run `git diff` and `git status` to ensure no stray files or unintended modifications are included.
3. **Reference Issues:** Always cite the issue number (`#<id>`) in the commit header or body to ensure full traceability on GitHub.
