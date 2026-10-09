-- nvchat — plugin bootstrap.
--
-- setup() is called from the config entry point (init.lua). It marks the
-- plugin as loaded and schedules the chat UI. The UI start is scheduled so
-- that headless runs which quit immediately (nvim --headless +q) never
-- reach it, and so the config itself loads fast.

local M = {}

function M.setup()
  vim.g.nvchat_loaded = true

  vim.schedule(function()
    -- vim.g.nvchat_no_ui lets tests and headless probes boot the config
    -- without building the chat screen.
    if not vim.g.nvchat_no_ui then
      require("nvchat.ui").start()
    end
  end)
end

return M
