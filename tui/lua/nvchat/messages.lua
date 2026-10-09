-- nvchat — the message list.
--
-- Renders the open session as message blocks: a sender row (sender + time),
-- then the body. Selection, yank and reply pinning live here too.

local M = {}

local state = require("nvchat.state")
local render = require("nvchat.render")

-- render() -> draws the open session's messages; records block ranges in
-- state.message_blocks so selection and yank can address whole blocks.
function M.render()
  local lines = {}
  state.message_blocks = {}

  if state.current then
    for _, message in ipairs(state.current.messages) do
      local first = #lines + 1
      table.insert(lines, message.sender .. "  " .. message.time)
      for _, body_line in ipairs(vim.split(message.body, "\n", { plain = true })) do
        table.insert(lines, body_line)
      end
      table.insert(lines, "")
      state.message_blocks[#state.message_blocks + 1] = {
        first = first,
        last = #lines - 1,
        message = message,
      }
    end
  end

  render.set_lines(state.bufs.messages, lines)

  -- Sender rows and times are attribute-decorated plain text (D10).
  local ns = state.ns
  if ns then
    vim.api.nvim_buf_clear_namespace(state.bufs.messages, ns, 0, -1)
    for _, block in ipairs(state.message_blocks) do
      local sender = block.message.sender
      local time = block.message.time
      local sender_hl = sender == "system" and "NvchatSystem" or "NvchatSender"
      vim.api.nvim_buf_set_extmark(state.bufs.messages, ns, block.first - 1, 0, {
        end_col = #sender,
        hl_group = sender_hl,
      })
      vim.api.nvim_buf_set_extmark(state.bufs.messages, ns, block.first - 1, #sender + 2, {
        end_col = #sender + 2 + #time,
        hl_group = "NvchatTime",
      })
    end
  end

  M.highlight_selection()
end

-- highlight_selection() -> paints the message block under the cursor. The
-- selection is the whole block, sender row included (D3). Separator lines
-- between blocks select nothing; the cursor does not snap.
function M.highlight_selection()
  local win = state.wins.messages
  if not win or not vim.api.nvim_win_is_valid(win) then
    return
  end
  local ns = state.ns_sel
  if not ns then
    return
  end
  vim.api.nvim_buf_clear_namespace(state.bufs.messages, ns, 0, -1)

  local line = vim.api.nvim_win_get_cursor(win)[1]
  for _, block in ipairs(state.message_blocks) do
    if line >= block.first and line <= block.last then
      for l = block.first, block.last do
        vim.api.nvim_buf_set_extmark(state.bufs.messages, ns, l - 1, 0, {
          line_hl_group = "NvchatSelection",
        })
      end
      M.keep_block_visible(block)
      return
    end
  end
end

-- keep_block_visible(block) -> scrolls just enough that the whole selected
-- block stays in view, when the block fits the window at all.
function M.keep_block_visible(block)
  local win = state.wins.messages
  if not win or not vim.api.nvim_win_is_valid(win) then
    return
  end
  local height = vim.api.nvim_win_get_height(win)
  local block_height = block.last - block.first + 1
  if block_height > height then
    return
  end
  local topline = vim.fn.line("w0", win)
  local botline = vim.fn.line("w$", win)
  local new_top
  if block.first < topline then
    new_top = block.first
  elseif block.last > botline then
    new_top = block.last - height + 1
  end
  if new_top then
    vim.api.nvim_win_call(win, function()
      vim.fn.winrestview({ topline = new_top })
    end)
  end
end

-- is_newest_in_view() -> true when the newest message block is fully
-- visible in the message window. Used to decide whether a new message may
-- pin the view (D5).
function M.is_newest_in_view()
  local win = state.wins.messages
  if not win or not vim.api.nvim_win_is_valid(win) then
    return false
  end
  local newest = state.message_blocks[#state.message_blocks]
  if not newest then
    return true
  end
  local topline = vim.fn.line("w0", win)
  local botline = vim.fn.line("w$", win)
  return topline <= newest.first and botline >= newest.last
end

-- refresh(pin) -> re-render after a message was appended; pinning moves the
-- view to the newest message, otherwise the view stays where it was (D5).
function M.refresh(pin)
  M.render()
  if pin then
    M.show_newest()
  end
  vim.cmd("redrawstatus")
end

-- show_newest() -> puts the cursor on the newest message and keeps it in
-- view. Opening a session always lands here (chat.pseudo, "open session").
function M.show_newest()
  local win = state.wins.messages
  if not win or not vim.api.nvim_win_is_valid(win) then
    return
  end
  local newest = state.message_blocks[#state.message_blocks]
  if not newest then
    return
  end
  vim.api.nvim_win_call(win, function()
    vim.cmd("normal! G")
  end)
  vim.api.nvim_win_set_cursor(win, { newest.first, 0 })
  M.highlight_selection()
end

-- block_at_cursor() -> the message block the cursor is inside, or nil on
-- a separator line.
local function block_at_cursor()
  local win = state.wins.messages
  if not win or not vim.api.nvim_win_is_valid(win) then
    return nil
  end
  local line = vim.api.nvim_win_get_cursor(win)[1]
  for _, block in ipairs(state.message_blocks) do
    if line >= block.first and line <= block.last then
      return block
    end
  end
  return nil
end

-- yank_block() -> yanks the whole selected block (sender row and body,
-- without the trailing blank). Copy is the job: the block always lands on
-- the unnamed register and the system clipboard; a named register the
-- writer asked for ("ay) receives it too. A stale v:register must never
-- stop the clipboard copy.
function M.yank_block()
  local block = block_at_cursor()
  if not block then
    return
  end
  local lines = vim.api.nvim_buf_get_lines(
    state.bufs.messages,
    block.first - 1,
    block.last,
    false
  )
  vim.fn.setreg('"', lines, "V")
  local ok, err = pcall(vim.fn.setreg, "+", lines, "V")
  if not ok then
    vim.notify("nvchat: clipboard unavailable: " .. tostring(err), vim.log.levels.WARN)
  end
  local register = vim.v.register
  if register and register ~= "" and register ~= '"' and register ~= "+" and register ~= "*" then
    vim.fn.setreg(register, lines, "V")
  end
end

-- line_in_block(line) -> the block containing a buffer line, or nil.
local function line_in_block(line)
  for _, block in ipairs(state.message_blocks) do
    if line >= block.first and line <= block.last then
      return block
    end
  end
  return nil
end

-- move(delta) -> cursor movement for the message list: line to line inside
-- a block, block to block across blocks. Separator lines are skipped.
local function move(delta)
  local win = state.wins.messages
  if not win or not vim.api.nvim_win_is_valid(win) then
    return
  end
  local total = vim.api.nvim_buf_line_count(state.bufs.messages)
  for _ = 1, vim.v.count1 do
    local target = vim.api.nvim_win_get_cursor(win)[1] + delta
    while target >= 1 and target <= total and not line_in_block(target) do
      target = target + delta
    end
    if target < 1 or target > total then
      return
    end
    vim.api.nvim_win_set_cursor(win, { target, 0 })
  end
end

function M.move_down()
  move(1)
end

function M.move_up()
  move(-1)
end

-- attach() -> buffer-local behavior of the message list.
function M.attach()
  vim.api.nvim_create_autocmd({ "CursorMoved", "WinEnter" }, {
    buffer = state.bufs.messages,
    callback = M.highlight_selection,
    desc = "nvchat: select the message block under the cursor",
  })
  vim.keymap.set("n", "j", M.move_down, {
    buffer = state.bufs.messages,
    desc = "nvchat: next message line",
  })
  vim.keymap.set("n", "k", M.move_up, {
    buffer = state.bufs.messages,
    desc = "nvchat: previous message line",
  })
  vim.keymap.set("n", "y", M.yank_block, {
    buffer = state.bufs.messages,
    desc = "nvchat: yank the selected message block",
  })
  vim.keymap.set("n", "yy", M.yank_block, {
    buffer = state.bufs.messages,
    desc = "nvchat: yank the selected message block",
  })
  vim.keymap.set("x", "y", function()
    M.yank_block()
    -- The block is the unit (D3): a visual yank copies it whole and leaves
    -- visual mode instead of keeping the character-wise selection.
    local esc = vim.api.nvim_replace_termcodes("<Esc>", true, false, true)
    vim.api.nvim_feedkeys(esc, "n", false)
  end, {
    buffer = state.bufs.messages,
    desc = "nvchat: yank the selected message block",
  })
end

return M
