-- nvchat — the files panel (real neo-tree, D19).
--
-- Owns neo-tree's configuration and the panel commands the keymaps call.
-- The panel is a left, full-height split over the filesystem; chosen files
-- open into normal editor windows, placed by ui.lua.

local M = {}

local WIDTH = 30
local configured = false

-- available() -> true while neo-tree is installed. The panel is skipped
-- instead of breaking the chat when the dependency is missing.
local function available()
  return (pcall(require, "neo-tree"))
end

-- width() -> the panel's configured width.
function M.width()
  return WIDTH
end

-- window() -> the neo-tree window in the current tab, or nil.
function M.window()
  for _, win in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
    local ft = vim.bo[vim.api.nvim_win_get_buf(win)].filetype
    if ft == "neo-tree" or ft:match("^neo%-tree") then
      return win
    end
  end
  return nil
end

-- setup() -> configures neo-tree once, before the panel is first shown.
-- Idempotent: a screen reload keeps this module cached and calls it again.
function M.setup()
  if configured or not available() then
    return
  end
  configured = true
  require("neo-tree").setup({
    sources = { "filesystem" },
    window = {
      position = "left",
      width = WIDTH,
    },
    filesystem = {
      filtered_items = { visible = false },
    },
  })
end

-- show() -> opens the panel (left dock), leaving focus on it.
function M.show()
  if not available() then
    return
  end
  require("neo-tree.command").execute({
    action = "show",
    source = "filesystem",
  })
end

-- focus() -> opens the panel if hidden and puts the keyboard on it.
function M.focus()
  if not available() then
    return
  end
  require("neo-tree.command").execute({
    action = "focus",
    source = "filesystem",
  })
end

-- toggle() -> shows or hides the panel.
function M.toggle()
  if not available() then
    return
  end
  -- This neo-tree version takes `toggle` as a flag; an "action" value of
  -- "toggle" silently falls through to focus.
  require("neo-tree.command").execute({
    toggle = true,
    source = "filesystem",
  })
end

return M
