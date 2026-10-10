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

-- D24: a visual range snaps to whole blocks. A second exchange gives the
-- list several blocks; a range that starts inside the second block's body
-- must still copy that block whole, and ggVGy must copy everything.
vim.api.nvim_buf_set_lines(state.bufs.composer, 0, -1, false, { "second ui" })
vim.bo[state.bufs.composer].modified = false
composer.send()
vim.wait(60000, function()
  return #chat.messages >= 4
end, 20)
messages.render()
messages.show_newest()

local function occurrences(text, word)
  local _, n = text:gsub(word, "")
  return n
end

local second = state.message_blocks[2]
yank_keys((second.first + 1) .. "GVGy")
local ranged = vim.fn.getreg('"')
check(
  "a ranged yank covers whole blocks from inside the first one",
  ranged:sub(1, 9) == "assistant"
    and occurrences(ranged, "assistant") == 2
    and ranged:find("second ui", 1, true) ~= nil
)
yank_keys("ggVGy")
local whole = vim.fn.getreg('"')
check(
  "yank ggVGy copies the whole conversation",
  whole:sub(1, 3) == "you"
    and occurrences(whole, "assistant") == 2
    and whole:find("hello ui", 1, true) ~= nil
    and whole:find("second ui", 1, true) ~= nil
    and whole:find("\n\n", 1, true) ~= nil
)
check("the whole-conversation yank is the longer one", #whole > #ranged)

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

-- The jobs overlay (system #15): mod+j opens a read-only view of the
-- launch's jobs. This mock launch has none; the tree shaping and the peek
-- refusal are covered by store_test.
local jobs = require("nvchat.jobs")
jobs.toggle()
check(
  "mod+j opens the jobs overlay",
  fresh.wins.jobs ~= nil and vim.api.nvim_win_is_valid(fresh.wins.jobs)
)
vim.wait(5000, function()
  local lines = vim.api.nvim_buf_get_lines(fresh.bufs.jobs, 0, -1, false)
  return lines[1] == "no jobs in this launch"
end, 20)
local job_lines = vim.api.nvim_buf_get_lines(fresh.bufs.jobs, 0, -1, false)
check("the empty launch renders an empty jobs tree", job_lines[1] == "no jobs in this launch")
check("the jobs overlay is read-only", vim.bo[fresh.bufs.jobs].modifiable == false)
jobs.close()
check("closing the jobs overlay hides it", fresh.wins.jobs == nil)

-- Markdown rendering (Issue #24 & #26): buffer text is untouched Markdown with markdown filetype
local fresh_messages = require("nvchat.messages")
local current_chat = fresh.current
table.insert(current_chat.messages, {
  sender = "assistant",
  time = "12:34",
  body = "# Title\n\n**bold** and `code`\n\n- item 1\n\n| a | b |\n|---|---|\n| 1 | 2 |",
})
fresh_messages.render()
check("messages buffer has markdown filetype", vim.bo[fresh.bufs.messages].filetype == "markdown")

-- Block selection and yank preserves raw Markdown syntax tokens (D3, D24)
local md_block = fresh.message_blocks[#fresh.message_blocks]
fresh_messages.yank_blocks({ md_block })
local md_yanked = vim.fn.getreg('"')
check(
  "yank preserves untouched raw Markdown tokens",
  md_yanked:find("# Title", 1, true) ~= nil
    and md_yanked:find("**bold**", 1, true) ~= nil
    and md_yanked:find("`code`", 1, true) ~= nil
    and md_yanked:find("- item 1", 1, true) ~= nil
    and md_yanked:find("| a | b |", 1, true) ~= nil
)
check("message buffer retains untouched Markdown text", vim.api.nvim_buf_get_lines(fresh.bufs.messages, md_block.first, md_block.last, false)[1] == "# Title")

-- Messages with escaped \n sequences split into distinct buffer lines
table.insert(current_chat.messages, {
  sender = "assistant",
  time = "12:35",
  body = "First line\\nSecond line\\n- bullet item",
})
fresh_messages.render()
local escaped_block = fresh.message_blocks[#fresh.message_blocks]
local escaped_lines = vim.api.nvim_buf_get_lines(fresh.bufs.messages, escaped_block.first, escaped_block.last, false)
check(
  "escaped \\n sequences split into multiple buffer lines without literal \\n characters",
  #escaped_lines >= 3
    and escaped_lines[1] == "First line"
    and escaped_lines[2] == "Second line"
    and escaped_lines[3] == "- bullet item"
)

-- Selection highlight is only visible when the messages window is active
vim.api.nvim_set_current_win(fresh.wins.messages)
fresh_messages.highlight_selection()
local sel_marks_in_win = vim.api.nvim_buf_get_extmarks(fresh.bufs.messages, fresh.ns_sel, 0, -1, {})
check("selection highlight active when inside messages window", #sel_marks_in_win > 0)

vim.api.nvim_set_current_win(fresh.wins.composer)
fresh_messages.highlight_selection()
local sel_marks_out_win = vim.api.nvim_buf_get_extmarks(fresh.bufs.messages, fresh.ns_sel, 0, -1, {})
check("selection highlight cleared when outside messages window", #sel_marks_out_win == 0)

if failures > 0 then
  print(failures .. " check(s) failed")
  vim.cmd("cquit 3")
else
  print("ui checks passed")
  vim.cmd("qa!")
end
