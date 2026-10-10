-- nvchat — configuration entry point.
--
-- Loaded by Neovim when NVIM_APPNAME=nvchat (see bin/nvchat and install.sh).
-- The sandbox is intentionally small: this file only hands control to the
-- plugin module under lua/nvchat/.

-- mod = <Space> (D15): the chat's keymap hangs off this leader.
vim.g.mapleader = " "

-- The core binary the store spawns: NVCHAT_CORE beats the default, which is
-- `core-agent` on PATH (install.sh builds it beside the nvchat wrapper). A
-- `g:nvchat_core` set before the config loads still wins.
local core = vim.env.NVCHAT_CORE
if core and core ~= "" and not vim.g.nvchat_core then
  vim.g.nvchat_core = core
end
local core_args = vim.env.NVCHAT_CORE_ARGS
if core_args and core_args ~= "" and not vim.g.nvchat_core_args then
  vim.g.nvchat_core_args = vim.split(core_args, "%s+", { trimempty = true })
end

-- Dependencies (D19): real neo-tree, installed by Neovim's own package
-- manager under the nvchat appname (stdpath("data")/site). The version floor
-- is Neovim 0.12 because of vim.pack.
if vim.pack and vim.pack.add then
  local ok, err = pcall(vim.pack.add, {
    {
      src = "https://github.com/nvim-neo-tree/neo-tree.nvim",
      version = vim.version.range("3"),
    },
    "https://github.com/nvim-lua/plenary.nvim",
    "https://github.com/MunifTanjim/nui.nvim",
    "https://github.com/MeanderingProgrammer/render-markdown.nvim",
  }, { confirm = false })
  if not ok then
    vim.notify("nvchat: plugin install failed: " .. tostring(err), vim.log.levels.ERROR)
  end
else
  vim.notify("nvchat requires Neovim 0.12+ (vim.pack)", vim.log.levels.ERROR)
end

require("nvchat").setup()
