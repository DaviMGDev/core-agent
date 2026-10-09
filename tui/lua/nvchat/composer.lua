-- nvchat — the composer.
--
-- The composer is a real editor buffer: a normal, modifiable buffer edited
-- with ordinary Neovim motions. This module owns the draft helpers and the
-- send action.

local M = {}

local state = require("nvchat.state")
local store = require("nvchat.store")

-- text() -> the draft as one string.
function M.text()
  local lines = vim.api.nvim_buf_get_lines(state.bufs.composer, 0, -1, false)
  return table.concat(lines, "\n")
end

-- clear() -> the draft becomes a single empty line.
function M.clear()
  vim.api.nvim_buf_set_lines(state.bufs.composer, 0, -1, false, { "" })
  vim.bo[state.bufs.composer].modified = false
end

-- put(text) -> replaces the draft with text, the cursor at its end. Used
-- when a session opens and its saved draft is restored (D18).
function M.put(text)
  if text == nil or text == "" then
    M.clear()
    return
  end
  local buf = state.bufs.composer
  local lines = vim.split(text, "\n", { plain = true })
  vim.api.nvim_buf_set_lines(buf, 0, -1, false, lines)
  vim.bo[buf].modified = false
  local win = state.wins.composer
  if win and vim.api.nvim_win_is_valid(win) then
    vim.api.nvim_win_set_cursor(win, { #lines, #lines[#lines] })
  end
end

-- send() -> hand the draft to the session. An empty draft changes
-- nothing; otherwise the message is echoed locally, the draft is cleared,
-- and the store is asked to deliver it (the reply lands asynchronously).
function M.send()
  local ui = require("nvchat.ui")
  local messages = require("nvchat.messages")

  local buf = state.bufs.composer
  if not buf or not vim.bo[buf].modifiable then
    -- A dead link froze the composer (tui.pseudo): sending is refused.
    return
  end

  local text = M.text()
  if text == "" then
    ui.focus_composer()
    return
  end

  local session = state.current
  if not session then
    return
  end

  store.deliver(session, text, {
    on_echo = function()
      messages.refresh(messages.is_newest_in_view())
    end,
    on_reply = function()
      messages.refresh(messages.is_newest_in_view())
    end,
  })

  M.clear()
  require("nvchat.draft").put(session, "")
  ui.focus_composer()
  -- The writer keeps typing after a send (D15): Enter sends, and the
  -- composer is where the next draft starts.
  vim.cmd.startinsert()
end

-- attach() -> the composer's buffer-local keys (D15). Enter sends from
-- insert and normal mode; ctrl+j and ctrl+enter insert a line break
-- instead (ctrl+enter only where the terminal reports it). The line-break
-- mappings use a non-recursive <CR> so they never reach the send mapping.
function M.attach()
  local buf = state.bufs.composer
  vim.keymap.set("n", "<CR>", M.send, {
    buffer = buf,
    desc = "nvchat: send the draft",
  })
  vim.keymap.set("i", "<CR>", M.send, {
    buffer = buf,
    desc = "nvchat: send the draft",
  })
  vim.keymap.set("i", "<C-j>", "<CR>", {
    buffer = buf,
    desc = "nvchat: insert a line break",
  })
  vim.keymap.set("i", "<C-CR>", "<CR>", {
    buffer = buf,
    desc = "nvchat: insert a line break",
  })

  -- The composer never counts as a modification (D18): the draft is saved
  -- at exit and on session switches, so :q/:qa must never stop here.
  vim.api.nvim_create_autocmd({ "TextChanged", "TextChangedI", "InsertLeave" }, {
    buffer = buf,
    callback = function()
      vim.bo[buf].modified = false
    end,
    desc = "nvchat: the draft never counts as a modification",
  })
end

return M
