-- nvchat — the sessions overlay.
--
-- The chat browser is a floating picker (D20): mod+s summons it above the
-- workspace, the rows keep their cursor across openings (D4), and choosing
-- a chat opens it and closes the overlay. The launch's chats are flat
-- (tui.pseudo): no threads, no groups, but a New chat row.

local M = {}

local state = require("nvchat.state")
local render = require("nvchat.render")
local store = require("nvchat.store")

local MIN_WIDTH = 44
local MAX_WIDTH = 64

-- node_at_cursor() -> the row node under the overlay cursor, or nil.
local function node_at_cursor()
  local win = state.wins.overlay
  if not win or not vim.api.nvim_win_is_valid(win) then
    return nil
  end
  local line = vim.api.nvim_win_get_cursor(win)[1]
  for _, entry in ipairs(state.tree_nodes or {}) do
    if entry.line == line then
      return entry
    end
  end
  return nil
end

-- refresh() -> re-lists the chats from the store and redraws the overlay.
function M.refresh()
  state.sessions = store.list()
  M.render()
end

-- choose() -> act on the row under the cursor: a chat opens and the overlay
-- closes; New chat creates a blank chat, opens it, and closes (tui.pseudo).
function M.choose()
  local entry = node_at_cursor()
  if not entry then
    return
  end
  local ui = require("nvchat.ui")
  if entry.kind == "chat" then
    ui.open_session(entry.node)
    M.close()
  elseif entry.kind == "new" then
    local chat = store.create()
    M.refresh()
    ui.open_session(chat)
    M.close()
  end
end

-- render() -> draws the flat chat rows and the New chat row. Records every
-- node's line so keys can address it.
function M.render()
  if not state.bufs.overlay then
    return
  end

  local lines = {}
  local nodes = {}

  for _, chat in ipairs(state.sessions) do
    table.insert(lines, chat.name)
    table.insert(nodes, { line = #lines, kind = "chat", node = chat })
  end

  table.insert(lines, "")
  table.insert(lines, "New chat")
  table.insert(nodes, { line = #lines, kind = "new" })

  state.tree_nodes = nodes
  render.set_lines(state.bufs.overlay, lines)

  -- Attribute decoration over the plain text (D10): the open chat.
  local ns = state.ns
  if ns then
    vim.api.nvim_buf_clear_namespace(state.bufs.overlay, ns, 0, -1)
    if state.current then
      for _, entry in ipairs(nodes) do
        if entry.kind == "chat" and entry.node == state.current then
          vim.api.nvim_buf_set_extmark(state.bufs.overlay, ns, entry.line - 1, 0, {
            line_hl_group = "NvchatActive",
          })
        end
      end
    end
  end
end

-- open() -> shows the floating picker, focused, with its remembered
-- cursor; opening an already-open overlay just refocuses it.
function M.open()
  local win = state.wins.overlay
  if win and vim.api.nvim_win_is_valid(win) then
    vim.api.nvim_set_current_win(win)
    return
  end

  M.render()

  local lines = vim.api.nvim_buf_line_count(state.bufs.overlay)
  local width = MIN_WIDTH
  for _, line in ipairs(vim.api.nvim_buf_get_lines(state.bufs.overlay, 0, -1, false)) do
    width = math.max(width, vim.fn.strdisplaywidth(line) + 4)
  end
  width = math.min(width, MAX_WIDTH, vim.o.columns - 4)
  local height = math.min(lines, vim.o.lines - 4)
  local row = math.max(0, math.floor((vim.o.lines - height) / 2) - 1)
  local col = math.max(0, math.floor((vim.o.columns - width) / 2))

  win = vim.api.nvim_open_win(state.bufs.overlay, true, {
    relative = "editor",
    width = width,
    height = height,
    row = row,
    col = col,
    style = "minimal",
    border = "single",
    title = " chats ",
    title_pos = "left",
    footer = " j/k · enter · esc ",
    footer_pos = "right",
  })
  state.wins.overlay = win
  vim.wo[win].cursorline = true
  vim.wo[win].wrap = false

  if state.overlay_cursor then
    vim.api.nvim_win_set_cursor(win, state.overlay_cursor)
  elseif state.current then
    for _, entry in ipairs(state.tree_nodes or {}) do
      if entry.kind == "chat" and entry.node == state.current then
        vim.api.nvim_win_set_cursor(win, { entry.line, 0 })
        break
      end
    end
  end
end

-- close() -> hides the overlay, remembering where its cursor was.
function M.close()
  local win = state.wins.overlay
  if win and vim.api.nvim_win_is_valid(win) then
    state.overlay_cursor = vim.api.nvim_win_get_cursor(win)
    vim.api.nvim_win_close(win, true)
  end
  state.wins.overlay = nil
end

-- toggle() -> mod+s: summon or dismiss the overlay.
function M.toggle()
  local win = state.wins.overlay
  if win and vim.api.nvim_win_is_valid(win) then
    M.close()
  else
    M.open()
  end
end

-- attach() -> the overlay buffer's keys.
function M.attach()
  local buf = state.bufs.overlay
  vim.keymap.set("n", "<CR>", M.choose, {
    buffer = buf,
    desc = "nvchat: open (or create) the chat under the cursor",
  })
  vim.keymap.set("n", "<Esc>", M.close, {
    buffer = buf,
    desc = "nvchat: close the sessions overlay",
  })
  vim.keymap.set("n", "q", M.close, {
    buffer = buf,
    desc = "nvchat: close the sessions overlay",
  })
end

return M
