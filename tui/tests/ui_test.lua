-- Headless scripted check for the whole screen (leaf 5.2): boot the real
-- nvchat UI against a spawned core, send a line through the composer, and
-- see the reply land in the open chat.

package.path = "tui/lua/?.lua;tui/lua/?/init.lua;" .. package.path

local failures = 0
local function check(name, cond)
  if cond then
    print("ok: " .. name)
  else
    failures = failures + 1
    print("FAIL: " .. name)
  end
end

local ui = require("nvchat.ui")
ui.start()

local state = require("nvchat.state")
local store = require("nvchat.store")
local composer = require("nvchat.composer")

check("the screen opened one blank chat", state.current ~= nil and state.current.name == "Chat 1")
check("the store spawned the core", store.job_id() > 0)

vim.api.nvim_buf_set_lines(state.bufs.composer, 0, -1, false, { "hello ui" })
vim.bo[state.bufs.composer].modified = false
composer.send()

local chat = state.current
vim.wait(60000, function()
  return #chat.messages >= 2
end, 20)
check(
  "the send echoed locally as the writer",
  chat.messages[1] ~= nil and chat.messages[1].sender == "you" and chat.messages[1].body == "hello ui"
)
check(
  "the reply landed through the composer path",
  chat.messages[2] ~= nil and chat.messages[2].sender == "assistant" and chat.messages[2].body ~= ""
)

-- Yank through the actual keys: the whole block lands on both registers,
-- in normal mode and in visual mode.
local messages = require("nvchat.messages")
messages.render()
messages.show_newest()
local function yank_keys(keys)
  vim.fn.setreg('"', {})
  vim.fn.setreg("+", {})
  vim.fn.setreg("a", {})
  vim.api.nvim_set_current_win(state.wins.messages)
  vim.cmd("stopinsert")
  vim.api.nvim_feedkeys(keys, "x", false)
end
yank_keys("y")
local yanked = vim.fn.getreg('"')
check(
  "yank y copies the whole block",
  yanked:find("hello ui", 1, true) ~= nil and yanked:find("assistant", 1, true) ~= nil
)
yank_keys("vy")
local visual = vim.fn.getreg('"')
check(
  "yank vy copies the whole block too",
  visual:find("hello ui", 1, true) ~= nil and visual:find("assistant", 1, true) ~= nil
)
yank_keys('"ay')
check(
  'yank "ay honors register a and still reaches the clipboard',
  vim.fn.getreg("a"):find("hello ui", 1, true) ~= nil
    and vim.fn.getreg("+"):find("hello ui", 1, true) ~= nil
)
local clip_ok, clip = pcall(vim.fn.getreg, "+")
check(
  "yank reaches the system clipboard",
  clip_ok and type(clip) == "string" and clip:find("hello ui", 1, true) ~= nil
)

-- Reload: the screen is rebuilt in place; the core and chats survive.
local old_msgs = state.bufs.messages
local core = store.job_id()
vim.cmd("Nvchat reload")
local fresh = require("nvchat.state")
check(
  "reload rebuilt the screen buffers",
  fresh ~= state and fresh.bufs.messages ~= old_msgs and vim.api.nvim_buf_is_valid(fresh.bufs.messages)
)
check("reload kept the core", store.job_id() == core and core > 0)
check("reload kept the chats", fresh.current ~= nil and #fresh.sessions == 1)

if failures > 0 then
  print(failures .. " check(s) failed")
  vim.cmd("cquit 3")
else
  print("ui checks passed")
  vim.cmd("qa!")
end
