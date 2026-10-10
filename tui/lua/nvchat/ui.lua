-- nvchat — the chat screen.
--
-- The main area holds the chat column (message list above, composer below)
-- and normal editor windows; the files panel is neo-tree, docked left
-- (files.lua). The chat is placed automatically (D21): full width while no
-- editor window is open, a right column while one is, and hidden with mod+h.

local M = {}

local state = require("nvchat.state")
local store = require("nvchat.store")

local COMPOSER_HEIGHT = 6
local CHAT_WIDTH = 50

-- Window options shared by every chat region.
local BASE_WIN_OPTS = {
  number = false,
  relativenumber = false,
  signcolumn = "no",
  foldcolumn = "0",
  cursorline = false,
  spell = false,
  list = false,
}

local function win_opts(win, opts)
  for name, value in pairs(opts) do
    vim.api.nvim_set_option_value(name, value, { win = win })
  end
end

local MESSAGES_OPTS = vim.tbl_extend("force", BASE_WIN_OPTS, {
  wrap = true,
  linebreak = true,
  conceallevel = 2,
  concealcursor = "",
})
local COMPOSER_OPTS = vim.tbl_extend("force", BASE_WIN_OPTS, {
  wrap = true,
  winfixheight = true,
})

-- Restrained highlight groups: terminal-native attribute decoration, linked
-- to the colorscheme so a future DESIGN.md can override them (D7, D10).
local function define_highlights()
  vim.api.nvim_set_hl(0, "NvchatTitle", { link = "Title", default = true })
  vim.api.nvim_set_hl(0, "NvchatSender", { link = "Title", default = true })
  vim.api.nvim_set_hl(0, "NvchatSystem", { link = "DiagnosticInfo", default = true })
  vim.api.nvim_set_hl(0, "NvchatTime", { link = "Comment", default = true })
  vim.api.nvim_set_hl(0, "NvchatActive", { link = "Visual", default = true })
  vim.api.nvim_set_hl(0, "NvchatSelection", { link = "Visual", default = true })
  vim.api.nvim_set_hl(0, "NvchatButton", { link = "Comment", default = true })
end

-- A rendered region buffer: plain text, never edited directly.
local function scratch_buffer(name)
  local buf = vim.api.nvim_create_buf(false, true)
  vim.api.nvim_buf_set_name(buf, name)
  vim.bo[buf].modifiable = false
  return buf
end

-- The composer is a real editor buffer: a normal buffer, editable with
-- normal Neovim motions.
local function composer_buffer()
  local buf = vim.api.nvim_create_buf(false, false)
  vim.api.nvim_buf_set_name(buf, "nvchat://composer")
  vim.bo[buf].bufhidden = "hide"
  vim.bo[buf].swapfile = false
  vim.api.nvim_buf_set_lines(buf, 0, -1, false, { "" })
  vim.bo[buf].modified = false
  return buf
end

-- -- statusline ----------------------------------------------------------

local MODE_NAMES = {
  n = "NORMAL",
  i = "INSERT",
  v = "VISUAL",
  V = "V-LINE",
  ["\22"] = "V-BLOCK",
  c = "COMMAND",
  R = "REPLACE",
  s = "SELECT",
  S = "S-LINE",
  t = "TERMINAL",
}

local function mode_text()
  local mode = vim.api.nvim_get_mode().mode
  return MODE_NAMES[mode] or mode:upper()
end

-- statusline() -> the global statusline: editor mode on the left, the open
-- session summary on the right.
function M.statusline()
  local left = " -- " .. mode_text() .. " --"
  local right = ""
  if state.current then
    local count = #state.current.messages
    right = ("%s — %d message%s"):format(
      state.current.name,
      count,
      count == 1 and "" or "s"
    )
  end
  return left .. "%=" .. right .. " "
end

-- -- chat placement ------------------------------------------------------

-- is_workspace_buffer(buf) -> true for a real file in a normal editor
-- window; nvchat's scratch buffers and neo-tree's own buffers do not count.
local function is_workspace_buffer(buf)
  if not vim.api.nvim_buf_is_valid(buf) then
    return false
  end
  if vim.bo[buf].buftype ~= "" then
    return false
  end
  local filetype = vim.bo[buf].filetype
  if filetype == "neo-tree" or filetype:match("^neo%-tree") then
    return false
  end
  local name = vim.api.nvim_buf_get_name(buf)
  if name == "" or name:match("^nvchat://") then
    return false
  end
  return true
end

-- workspace_windows() -> windows currently showing a workspace file.
local function workspace_windows()
  local wins = {}
  for _, win in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
    if is_workspace_buffer(vim.api.nvim_win_get_buf(win)) then
      wins[#wins + 1] = win
    end
  end
  return wins
end

-- chat_is_open() -> true while the message list window exists.
local function chat_is_open()
  local win = state.wins.messages
  return win ~= nil and vim.api.nvim_win_is_valid(win)
end

-- focus_composer() -> the keyboard goes to the draft.
function M.focus_composer()
  if not chat_is_open() then
    M.show_chat()
  end
  if state.wins.composer and vim.api.nvim_win_is_valid(state.wins.composer) then
    vim.api.nvim_set_current_win(state.wins.composer)
  end
end

-- focus_messages() -> the keyboard goes to the message list.
function M.focus_messages()
  if not chat_is_open() then
    M.show_chat()
  end
  if state.wins.messages and vim.api.nvim_win_is_valid(state.wins.messages) then
    vim.api.nvim_set_current_win(state.wins.messages)
  end
end

-- show_chat() -> (re)creates the chat column right of the current window:
-- message list above, composer below.
function M.show_chat()
  state.chat_visible = true
  if chat_is_open() then
    return
  end

  local prev = vim.api.nvim_get_current_win()

  vim.cmd("belowright vsplit")
  local messages = vim.api.nvim_get_current_win()
  state.wins.messages = messages
  vim.api.nvim_win_set_buf(messages, state.bufs.messages)
  win_opts(messages, MESSAGES_OPTS)

  vim.cmd("belowright split")
  local composer = vim.api.nvim_get_current_win()
  state.wins.composer = composer
  vim.api.nvim_win_set_buf(composer, state.bufs.composer)
  vim.api.nvim_win_set_height(composer, COMPOSER_HEIGHT)
  win_opts(composer, COMPOSER_OPTS)

  if vim.api.nvim_win_is_valid(prev) then
    vim.api.nvim_set_current_win(prev)
  end

  M.place_chat()
end

-- hide_chat() -> hides the chat column; whatever remains takes the screen.
function M.hide_chat()
  for _, win in ipairs({ state.wins.messages, state.wins.composer }) do
    if win and vim.api.nvim_win_is_valid(win) then
      vim.api.nvim_win_hide(win)
    end
  end
  state.wins.messages = nil
  state.wins.composer = nil
end

-- place_chat() -> the automatic placement (D21): full main area while no
-- editor window is open, a right column while one is, nothing while hidden.
function M.place_chat()
  if not state.chat_visible then
    return
  end
  if not chat_is_open() then
    M.show_chat()
    return
  end

  -- Keep the files panel at its width whenever the chat is placed.
  local files = require("nvchat.files")
  local files_win = files.window()
  if files_win and vim.api.nvim_win_is_valid(files_win) then
    vim.api.nvim_win_set_width(files_win, files.width())
  end

  local editors = workspace_windows()
  if #editors > 0 then
    vim.api.nvim_win_set_width(state.wins.messages, CHAT_WIDTH)
  end
  -- Without editor windows the chat already owns the remaining width.
end

-- toggle_chat() -> mod+h: hide or show the chat column.
function M.toggle_chat()
  state.chat_visible = not state.chat_visible
  if state.chat_visible then
    M.show_chat()
    M.place_chat()
  else
    M.hide_chat()
  end
end

-- -- regions -------------------------------------------------------------

-- open_session(session) -> makes one session current, swaps the composer
-- draft, shows and places the chat, and re-renders what depends on it.
-- Choosing the already-open session keeps the live draft untouched (D18).
function M.open_session(session)
  local composer = require("nvchat.composer")
  local draft = require("nvchat.draft")

  local swapping = state.current ~= session
  if swapping and state.current then
    draft.put(state.current, composer.text())
  end
  state.current = session
  -- The core owns the record: a chat's turns load when it first opens.
  store.load(session)
  if swapping then
    composer.put(draft.get(session))
  end
  state.chat_visible = true
  M.show_chat()
  require("nvchat.messages").render()
  require("nvchat.messages").show_newest()
  M.place_chat()
end

-- reload() -> rebuilds the screen in place. The screen modules (state and
-- the regions) are reloaded and the layout rebuilt, while the store (the
-- core link and the launch's chats) and the files panel stay as they are.
-- Edits to screen modules apply; store/files changes need a relaunch.
function M.reload()
  local state = require("nvchat.state")

  -- Drop the old region autocmds before the windows move.
  pcall(vim.api.nvim_del_augroup_by_name, "nvchat")

  if state.wins.overlay and vim.api.nvim_win_is_valid(state.wins.overlay) then
    pcall(vim.api.nvim_win_close, state.wins.overlay, true)
  end
  if state.wins.jobs and vim.api.nvim_win_is_valid(state.wins.jobs) then
    pcall(vim.api.nvim_win_close, state.wins.jobs, true)
  end
  if state.wins.composer and vim.api.nvim_win_is_valid(state.wins.composer) then
    pcall(vim.api.nvim_win_close, state.wins.composer, true)
  end
  if state.wins.messages and vim.api.nvim_win_is_valid(state.wins.messages) then
    vim.api.nvim_set_current_win(state.wins.messages)
  end
  for _, name in ipairs({ "messages", "composer", "overlay", "jobs" }) do
    local buf = state.bufs[name]
    if buf and vim.api.nvim_buf_is_valid(buf) then
      pcall(vim.api.nvim_buf_delete, buf, { force = true })
    end
  end

  for _, name in ipairs({
    "nvchat.state",
    "nvchat.ui",
    "nvchat.messages",
    "nvchat.composer",
    "nvchat.sessions",
    "nvchat.jobs",
  }) do
    package.loaded[name] = nil
  end
  require("nvchat.ui").start()
end

-- start() -> builds the screen. Called once, right after config load.
function M.start()
  if state.started then
    return
  end
  state.started = true
  -- All screen autocmds live in one group so a reload can drop them.
  local group = vim.api.nvim_create_augroup("nvchat", { clear = true })

  state.sessions = store.list()
  state.ns = vim.api.nvim_create_namespace("nvchat")
  state.ns_sel = vim.api.nvim_create_namespace("nvchat-selection")
  define_highlights()
  require("nvchat.files").setup()

  -- Screen-wide options: one global statusline, no tabline, no command row.
  vim.o.laststatus = 3
  vim.o.showmode = false
  vim.o.cmdheight = 0
  vim.o.showtabline = 0
  vim.o.fillchars = "vert:│,horiz:─,eob: "
  vim.o.splitright = true
  vim.o.splitbelow = true
  vim.o.statusline = "%!v:lua.require'nvchat.ui'.statusline()"

  state.bufs.messages = scratch_buffer("nvchat://messages")
  vim.bo[state.bufs.messages].filetype = "markdown"
  pcall(vim.treesitter.start, state.bufs.messages, "markdown")
  state.bufs.composer = composer_buffer()
  state.bufs.overlay = scratch_buffer("nvchat://sessions")
  state.bufs.jobs = scratch_buffer("nvchat://jobs")

  -- A dead link freezes the screen (tui.pseudo): the store reports it here.
  store.on_error = M.freeze

  -- Main area, top to bottom: message list, composer.
  local messages = vim.api.nvim_get_current_win()
  state.wins.messages = messages
  vim.api.nvim_win_set_buf(messages, state.bufs.messages)
  win_opts(messages, MESSAGES_OPTS)

  vim.cmd("belowright split")
  local composer = vim.api.nvim_get_current_win()
  state.wins.composer = composer
  vim.api.nvim_win_set_buf(composer, state.bufs.composer)
  vim.api.nvim_win_set_height(composer, COMPOSER_HEIGHT)
  win_opts(composer, COMPOSER_OPTS)

  -- Files panel (D19): real neo-tree docked left.
  require("nvchat.files").show()

  require("nvchat.messages").render()
  require("nvchat.messages").attach()
  require("nvchat.composer").attach()
  require("nvchat.sessions").attach()
  require("nvchat.jobs").attach()

  -- Global keys (D15, D19-D21): mod = <Space>. mod+E files, mod+S sessions
  -- (the overlay arrives with its module), mod+H hides the chat, and
  -- mod+T/M/C move the keyboard between the regions.
  vim.keymap.set({ "n", "x" }, "<leader>e", require("nvchat.files").toggle, {
    desc = "nvchat: toggle the files panel",
  })
  vim.keymap.set({ "n", "x" }, "<leader>t", require("nvchat.files").focus, {
    desc = "nvchat: focus the files panel",
  })
  vim.keymap.set({ "n", "x" }, "<leader>m", M.focus_messages, {
    desc = "nvchat: focus the message list",
  })
  vim.keymap.set({ "n", "x" }, "<leader>c", M.focus_composer, {
    desc = "nvchat: focus the composer",
  })
  vim.keymap.set({ "n", "x" }, "<leader>h", M.toggle_chat, {
    desc = "nvchat: hide or show the chat",
  })
  vim.keymap.set({ "n", "x" }, "<leader>s", require("nvchat.sessions").toggle, {
    desc = "nvchat: sessions overlay",
  })
  vim.keymap.set({ "n", "x" }, "<leader>j", require("nvchat.jobs").toggle, {
    desc = "nvchat: jobs overlay",
  })
  vim.keymap.set({ "n", "i" }, "<C-b>", require("nvchat.files").toggle, {
    desc = "nvchat: toggle the files panel",
  })
  vim.keymap.set({ "n", "x" }, "<leader>r", M.reload, {
    desc = "nvchat: reload the screen",
  })
  pcall(vim.api.nvim_del_user_command, "Nvchat")
  vim.api.nvim_create_user_command("Nvchat", function(opts)
    if opts.fargs[1] == "reload" then
      M.reload()
    else
      vim.notify("nvchat: unknown command (try :Nvchat reload)", vim.log.levels.WARN)
    end
  end, {
    nargs = "*",
    complete = function()
      return { "reload" }
    end,
    desc = "nvchat: reload the screen in place (keeps the core and chats)",
  })

  -- A file that lands in a chat window (neo-tree opens files in the last
  -- focused window) is moved to a new editor window left of the column.
  vim.api.nvim_create_autocmd("BufWinEnter", {
    group = group,
    callback = function(args)
      if not is_workspace_buffer(args.buf) then
        return
      end
      -- Defer: windows must not be restructured inside the event.
      vim.schedule(function()
        if not vim.api.nvim_buf_is_valid(args.buf) then
          return
        end
        -- A file that landed in a chat window displaces its buffer; put
        -- the chat back and open the file in a full-height editor column
        -- left of the chat.
        local files = require("nvchat.files")
        for _, w in ipairs(vim.fn.win_findbuf(args.buf)) do
          if w == state.wins.messages or w == state.wins.composer then
            local chatbuf = (w == state.wins.messages) and state.bufs.messages
              or state.bufs.composer
            vim.api.nvim_win_set_buf(w, chatbuf)

            local neo = files.window()
            if neo and vim.api.nvim_win_is_valid(neo) then
              -- split the full-height files panel: the editor column
              -- lands between the panel and the chat
              vim.api.nvim_set_current_win(neo)
              vim.cmd("belowright vsplit")
            else
              -- no panel: a full-height column at the far left
              vim.cmd("topleft vsplit")
            end
            local editor = vim.api.nvim_get_current_win()
            vim.api.nvim_win_set_buf(editor, args.buf)
            vim.api.nvim_set_current_win(editor)
            break
          end
        end
        M.place_chat()
      end)
    end,
    desc = "nvchat: place a newly opened file",
  })

  -- The chat re-places itself when a window closes: the last file closing
  -- hands the full width back to the chat.
  vim.api.nvim_create_autocmd("WinClosed", {
    group = group,
    callback = function()
      vim.schedule(function()
        M.place_chat()
      end)
    end,
    desc = "nvchat: re-place the chat after a window closes",
  })

  -- The columns re-fit when the terminal is resized.
  vim.api.nvim_create_autocmd("VimResized", {
    group = group,
    callback = function()
      vim.schedule(function()
        M.place_chat()
      end)
    end,
    desc = "nvchat: re-place the chat on resize",
  })

  -- The unsent draft is client-local state (D18): save it on the way out,
  -- keyed by the open session.
  vim.api.nvim_create_autocmd("VimLeavePre", {
    group = group,
    callback = function()
      require("nvchat.draft").put(state.current, require("nvchat.composer").text())
    end,
    desc = "nvchat: persist the unsent draft",
  })

  -- Start flow (chat.pseudo): list the chats, open the most recent one,
  -- focus the composer.
  local recent = store.recent()
  if recent then
    M.open_session(recent)
  end
  M.focus_composer()
  -- Continue the draft at its end: a restored multiline draft must not
  -- send the cursor back to the last character (D18).
  vim.cmd("startinsert!")
end

-- freeze(reason) -> the core link died (tui.pseudo): the transcript stops,
-- the failure renders in place, and the composer goes read-only. Recovery
-- is relaunching nvchat, so nothing here tries to heal the link.
function M.freeze(reason)
  local chat = state.current
  if chat then
    table.insert(chat.messages, {
      sender = "system",
      time = os.date("%H:%M"),
      body = "core link lost: " .. tostring(reason),
    })
  end
  if state.bufs.composer and vim.api.nvim_buf_is_valid(state.bufs.composer) then
    vim.bo[state.bufs.composer].modifiable = false
  end
  if state.bufs.messages and vim.api.nvim_buf_is_valid(state.bufs.messages) then
    require("nvchat.messages").refresh(true)
  end
end

return M
