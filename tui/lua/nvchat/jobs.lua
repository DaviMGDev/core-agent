-- nvchat — the jobs overlay.
--
-- The launch's jobs at a glance (system #15): a floating overlay (mod+j) in
-- the spirit of the sessions picker, showing the manager's job tree with the
-- selected job's peek — state, last tick, and output so far. It is
-- read-only: start, peep, kill, and the tick cadence stay with the tool
-- manager, and ticks never become transcript entries (tui.pseudo). Live
-- updates arrive as job transitions stream, and the overlay refreshes while
-- it is open.

local M = {}

local state = require("nvchat.state")
local render = require("nvchat.render")
local store = require("nvchat.store")

local MIN_WIDTH = 44
local MAX_WIDTH = 72
local MAX_PEEK_LINES = 12
local INDENT = 2 -- cells per tree depth

-- format_ms(ms) -> a compact age: 850ms, 12s, 3m04s, 1h02m.
local function format_ms(ms)
  ms = tonumber(ms) or 0
  if ms < 1000 then
    return ms .. "ms"
  end
  local s = math.floor(ms / 1000)
  if s < 60 then
    return s .. "s"
  end
  local m = math.floor(s / 60)
  if m < 60 then
    return ("%dm%02ds"):format(m, s % 60)
  end
  return ("%dh%02dm"):format(math.floor(m / 60), m % 60)
end

-- row_text(row, depth) -> one tree row: job id, tool, state, and age.
local function row_text(row, depth)
  local parts = { row.job or "?" }
  if row.tool and row.tool ~= "" then
    parts[#parts + 1] = row.tool
  end
  parts[#parts + 1] = row.state or "?"
  parts[#parts + 1] = format_ms(row.age_ms)
  return string.rep(" ", depth * INDENT) .. table.concat(parts, " ")
end

-- flatten(nodes, depth, lines, entries) -> the tree as text lines, with every
-- job row's line recorded so keys can address it.
local function flatten(nodes, depth, lines, entries)
  for _, node in ipairs(nodes) do
    lines[#lines + 1] = row_text(node.row, depth)
    entries[#entries + 1] = { line = #lines, kind = "job", row = node.row }
    flatten(node.children, depth + 1, lines, entries)
  end
end

-- selected() -> the job row under the overlay cursor, or nil.
local function selected()
  local win = state.wins.jobs
  if not win or not vim.api.nvim_win_is_valid(win) then
    return nil
  end
  local line = vim.api.nvim_win_get_cursor(win)[1]
  for _, entry in ipairs(state.job_nodes or {}) do
    if entry.line == line then
      return entry.row
    end
  end
  return nil
end

-- peek_lines(row) -> the preview block for the selected job's last peek.
local function peek_lines(row)
  if not row then
    return { "select a job to peek" }
  end
  if state.peek_errors and state.peek_errors[row.job] then
    return { "peek " .. row.job .. ": " .. state.peek_errors[row.job] }
  end
  local peek = store.peeked(row.job)
  if not peek then
    return { "peek " .. row.job .. "…" }
  end
  local lines = { ("peek %s — %s"):format(peek.job or "?", peek.state or "?") }
  if peek.tool and peek.tool ~= "" then
    lines[#lines + 1] = "tool: " .. peek.tool
  end
  lines[#lines + 1] = ("age: %s · idle: %s"):format(format_ms(peek.age_ms), format_ms(peek.idle_ms))
  if peek.last_tick and peek.last_tick ~= "" then
    lines[#lines + 1] = "last tick: " .. peek.last_tick
  end
  if peek.error and peek.error ~= "" then
    lines[#lines + 1] = "error: " .. peek.error
  end
  local output = (peek.output or ""):gsub("%s+$", "")
  if output ~= "" then
    lines[#lines + 1] = "output:"
    local count = 0
    for _, l in ipairs(vim.split(output, "\n", { plain = true })) do
      count = count + 1
      if count > MAX_PEEK_LINES then
        lines[#lines + 1] = "… (truncated)"
        break
      end
      lines[#lines + 1] = "  " .. l
    end
  end
  return lines
end

-- render() -> draws the job tree and the selected job's peek preview.
function M.render()
  if not state.bufs.jobs then
    return
  end
  local lines = {}
  local entries = {}
  local tree = store.job_tree(state.jobs or {})
  if #tree == 0 then
    lines[#lines + 1] = "no jobs in this launch"
  else
    flatten(tree, 0, lines, entries)
  end
  state.job_nodes = entries

  local row = selected() or (entries[1] and entries[1].row)
  lines[#lines + 1] = ""
  for _, l in ipairs(peek_lines(row)) do
    lines[#lines + 1] = l
  end

  render.set_lines(state.bufs.jobs, lines)

  -- Attribute decoration over the plain text (D10): the selected row.
  local ns = state.ns
  if ns then
    vim.api.nvim_buf_clear_namespace(state.bufs.jobs, ns, 0, -1)
    if row then
      for _, entry in ipairs(entries) do
        if entry.row.job == row.job then
          vim.api.nvim_buf_set_extmark(state.bufs.jobs, ns, entry.line - 1, 0, {
            line_hl_group = "NvchatActive",
          })
        end
      end
    end
  end

  -- The cursor must stay on a job row; preview lines are never selectable.
  local win = state.wins.jobs
  if win and vim.api.nvim_win_is_valid(win) and #entries > 0 then
    local cur = vim.api.nvim_win_get_cursor(win)[1]
    if not state.job_nodes[cur] then
      vim.api.nvim_win_set_cursor(win, { entries[1].line, 0 })
    end
  end
end

-- select() -> the cursor moved: ask for the selected job's peek.
function M.select()
  local row = selected()
  if not row then
    return
  end
  state.job_selected = row.job
  state.peek_errors = state.peek_errors or {}
  store.peek(row.job, {
    on_peek = function()
      state.peek_errors[row.job] = nil
      if state.wins.jobs and vim.api.nvim_win_is_valid(state.wins.jobs) then
        M.render()
      end
    end,
    on_error = function(reason)
      state.peek_errors[row.job] = reason
      if state.wins.jobs and vim.api.nvim_win_is_valid(state.wins.jobs) then
        M.render()
      end
    end,
  })
end

-- move(delta) -> the cursor steps to the next or previous job row.
local function move(delta)
  local entries = state.job_nodes or {}
  if #entries == 0 then
    return
  end
  local row = selected()
  local index = 1
  for i, entry in ipairs(entries) do
    if row and entry.row.job == row.job then
      index = i
    end
  end
  local target = math.max(1, math.min(#entries, index + delta))
  local win = state.wins.jobs
  if win and vim.api.nvim_win_is_valid(win) then
    vim.api.nvim_win_set_cursor(win, { entries[target].line, 0 })
    M.select()
  end
end

-- refresh() -> re-asks the store for the launch's jobs; the open overlay
-- redraws and re-peeks the selection when the answer arrives.
function M.refresh()
  store.jobs({
    on_jobs = function(rows)
      state.jobs = rows
      if state.wins.jobs and vim.api.nvim_win_is_valid(state.wins.jobs) then
        M.render()
        M.select()
      end
    end,
  })
end

-- on_transition() -> a job transition streamed in: refresh while the overlay
-- is open. Ticks never cross the link; the open peek follows real state
-- changes and the manual refresh.
function M.on_transition()
  if state.wins.jobs and vim.api.nvim_win_is_valid(state.wins.jobs) then
    M.refresh()
  end
end

-- open() -> shows the floating picker, focused, with its remembered cursor.
function M.open()
  local win = state.wins.jobs
  if win and vim.api.nvim_win_is_valid(win) then
    vim.api.nvim_set_current_win(win)
    return
  end

  -- Draw with the last known rows at once; refresh answers with the current
  -- tree and the peek.
  M.render()
  win = vim.api.nvim_open_win(state.bufs.jobs, true, {
    relative = "editor",
    width = MIN_WIDTH,
    height = math.min(10, vim.o.lines - 4),
    row = math.max(0, math.floor((vim.o.lines - 10) / 2) - 1),
    col = math.max(0, math.floor((vim.o.columns - MIN_WIDTH) / 2)),
    style = "minimal",
    border = "single",
    title = " jobs ",
    title_pos = "left",
    footer = " j/k · r · esc ",
    footer_pos = "right",
  })
  state.wins.jobs = win
  vim.wo[win].cursorline = true
  vim.wo[win].wrap = false

  if state.jobs_cursor then
    pcall(vim.api.nvim_win_set_cursor, win, state.jobs_cursor)
  end
  M.size()
  M.refresh()
end

-- size() -> fits the floating window to the content, within bounds.
function M.size()
  local win = state.wins.jobs
  if not win or not vim.api.nvim_win_is_valid(win) then
    return
  end
  local lines = vim.api.nvim_buf_get_lines(state.bufs.jobs, 0, -1, false)
  local width = MIN_WIDTH
  for _, line in ipairs(lines) do
    width = math.max(width, vim.fn.strdisplaywidth(line) + 4)
  end
  width = math.min(width, MAX_WIDTH, vim.o.columns - 4)
  local height = math.min(math.max(#lines, 3), vim.o.lines - 4)

  vim.api.nvim_win_set_config(win, {
    relative = "editor",
    width = width,
    height = height,
    row = math.max(0, math.floor((vim.o.lines - height) / 2) - 1),
    col = math.max(0, math.floor((vim.o.columns - width) / 2)),
  })
end

-- close() -> hides the overlay, remembering where its cursor was.
function M.close()
  local win = state.wins.jobs
  if win and vim.api.nvim_win_is_valid(win) then
    state.jobs_cursor = vim.api.nvim_win_get_cursor(win)
    vim.api.nvim_win_close(win, true)
  end
  state.wins.jobs = nil
end

-- toggle() -> mod+j: summon or dismiss the overlay.
function M.toggle()
  local win = state.wins.jobs
  if win and vim.api.nvim_win_is_valid(win) then
    M.close()
  else
    M.open()
  end
end

-- attach() -> the overlay buffer's keys and the live-update hook.
function M.attach()
  local buf = state.bufs.jobs
  if not buf then
    return
  end
  vim.keymap.set("n", "j", function()
    move(1)
  end, { buffer = buf, desc = "nvchat: next job" })
  vim.keymap.set("n", "k", function()
    move(-1)
  end, { buffer = buf, desc = "nvchat: previous job" })
  vim.keymap.set("n", "<Down>", function()
    move(1)
  end, { buffer = buf, desc = "nvchat: next job" })
  vim.keymap.set("n", "<Up>", function()
    move(-1)
  end, { buffer = buf, desc = "nvchat: previous job" })
  vim.keymap.set("n", "r", M.refresh, { buffer = buf, desc = "nvchat: refresh the jobs panel" })
  vim.keymap.set("n", "<Esc>", M.close, { buffer = buf, desc = "nvchat: close the jobs overlay" })
  vim.keymap.set("n", "q", M.close, { buffer = buf, desc = "nvchat: close the jobs overlay" })
  store.on_job = M.on_transition
end

return M
