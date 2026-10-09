-- Headless scripted checks for the core-backed store (leaves 3.1-3.5).
--
-- Runs inside Neovim (see run.sh) against the real `core-agent -tui` link
-- with the mock provider: the store's four operations, lazy chats, naming,
-- and the dead-core path.

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

local store = require("nvchat.store")

-- 3.1: one core per launch, spawned over stdio.
local list = store.list()
check(
  "a fresh launch lists one blank chat",
  #list == 1 and list[1].name == "Chat 1" and #list[1].messages == 0
)
check("the store spawned one core process", store.job_id() > 0)

-- 3.2: deliver crosses the link; the reply lands via handlers.on_reply.
local chat = store.recent()
check("recent is the launch's chat", chat == list[1])
local echoed, replied = false, nil
local message = store.deliver(chat, "hello store", {
  on_echo = function()
    echoed = true
  end,
  on_reply = function(_, reply)
    replied = reply
  end,
})
check(
  "deliver echoes locally before any ack",
  message.body == "hello store" and message.sender == "you" and #chat.messages == 1 and echoed
)
vim.wait(60000, function()
  return replied ~= nil
end, 20)
check(
  "the reply lands through handlers.on_reply",
  replied ~= nil and replied.sender == "assistant" and replied.body ~= "" and #chat.messages == 2
)

-- 3.3: naming is screen-side.
check("the first user line names the chat", chat.name == "hello store")
local second = store.create()
check("a new chat is Chat N", second.name == "Chat 2" and #store.list() == 2)
local second_reply = nil
store.deliver(second, "name b", {
  on_reply = function(_, r)
    second_reply = r
  end,
})
vim.wait(60000, function()
  return second_reply ~= nil
end, 20)
check("the second chat's name follows its first line", second.name == "name b")

-- 3.4: lazily-created chats deliver on their own ids and load their own
-- records.
local a_turns = store.load(chat)
local b_turns = store.load(second)
check("both chats load their own turns", #a_turns == 2 and #b_turns == 2)
check(
  "the records are distinct",
  a_turns[1].body == "hello store" and b_turns[1].body == "name b"
)

-- 3.5: an unreachable core reports an error instead of serving anything.
store.stop()
package.loaded["nvchat.store"] = nil
vim.g.nvchat_core = "/nonexistent/core-agent"
local dead = require("nvchat.store")
local dead_list = dead.list()
vim.wait(5000, function()
  return dead.error ~= nil
end, 20)
check("an unreachable core sets the store error", dead.error ~= nil)
check("an unreachable core serves nothing", #dead_list == 0)

if failures > 0 then
  print(failures .. " check(s) failed")
  vim.cmd("cquit 3")
else
  print("store checks passed")
  vim.cmd("qa!")
end
