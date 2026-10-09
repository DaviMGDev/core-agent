-- Clipboard/yank diagnostic for a live nvchat session.
-- Paste-safe: run it with  :luafile /home/davi/Projects/core-agent/tui/tests/clip_diag.lua
--
-- Prints which messages module the session loaded, the clipboard provider
-- environment, a direct clipboard write, and a real block yank through the
-- loaded code.

local m = require("nvchat.messages")
local state = require("nvchat.state")

print("file=" .. debug.getinfo(m.yank_block, "S").source)
print(
  "wenv="
    .. tostring(vim.env.WAYLAND_DISPLAY)
    .. " denv="
    .. tostring(vim.env.DISPLAY)
    .. " gclip="
    .. vim.inspect(vim.g.clipboard)
)
print("buffer=" .. vim.api.nvim_buf_get_name(0) .. " vreg=[" .. tostring(vim.v.register) .. "]")
print(
  "msgbuf="
    .. tostring(state.bufs.messages ~= nil and vim.api.nvim_buf_is_valid(state.bufs.messages))
    .. " msgwin="
    .. tostring(state.wins.messages ~= nil and vim.api.nvim_win_is_valid(state.wins.messages))
    .. " blocks="
    .. tostring(#(state.message_blocks or {}))
)
local win = state.wins.messages
if win and vim.api.nvim_win_is_valid(win) then
  local line = vim.api.nvim_win_get_cursor(win)[1]
  print("msg_cursor=" .. line)
  for i, b in ipairs(state.message_blocks or {}) do
    print("block" .. i .. "=" .. b.first .. "-" .. b.last .. (line >= b.first and line <= b.last and " <=cursor" or ""))
  end
end

local ok, err = pcall(vim.fn.setreg, "+", "probe")
print("direct_ok=" .. tostring(ok) .. " err=" .. tostring(err))
print("clip1=[" .. vim.fn.getreg("+") .. "]")

m.render()
m.show_newest()
m.yank_block()
print("clip2=[" .. vim.fn.getreg("+") .. "] unnamed2=[" .. vim.fn.getreg('"') .. "]")
print("mapping=" .. vim.fn.maparg("y", "n"))
