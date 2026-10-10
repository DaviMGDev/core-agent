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

-- 3.6: the read-only job queries. The mock launch has no jobs, so the
-- core answers an empty list; the tree builder is checked on scripted rows,
-- and an unknown peek answers a factual error.
local jobs_answer = nil
store.jobs({
  on_jobs = function(rows)
    jobs_answer = rows
  end,
})
vim.wait(5000, function()
  return jobs_answer ~= nil
end, 20)
check("the core answers a jobs request", jobs_answer ~= nil and #jobs_answer == 0)

local tree = store.job_tree({
  { job = "job-1", tool = "subagent", state = "running" },
  { job = "job-2", tool = "ok", state = "done", parent = "job-1" },
  { job = "job-3", tool = "subagent", state = "running", parent = "job-1" },
  { job = "job-4", tool = "ok", state = "done" },
})
check(
  "the tree nests children under their parent",
  #tree == 2
    and tree[1].row.job == "job-1"
    and #tree[1].children == 2
    and tree[1].children[1].row.job == "job-2"
    and tree[1].children[2].row.job == "job-3"
    and tree[2].row.job == "job-4"
)

local peek_error = nil
store.peek("job-99", {
  on_error = function(reason)
    peek_error = reason
  end,
})
vim.wait(5000, function()
  return peek_error ~= nil
end, 20)
check(
  "an unknown peek answers a factual error",
  peek_error ~= nil and string.find(peek_error, "unknown job", 1, true) ~= nil
)

-- 4.1: Human-readable job transitions in transcript without raw escaped JSON.
store._handle_line('{"kind":"job","event":"started","job":"job-10","tool":"bash","detail":"echo hello"}')
local recent_chat = store.recent()
local last_m = recent_chat.messages[#recent_chat.messages]
check(
  "job started transcript entry formats tool and brief",
  last_m.sender == "system" and last_m.body == "job job-10: started bash — echo hello"
)

store._handle_line([=[{"kind":"job","event":"completed","job":"job-11","tool":"subagent","detail":"{\"reply\":\"clean text\"}"}]=])
last_m = recent_chat.messages[#recent_chat.messages]
check(
  "job completed transcript entry unescapes reply JSON",
  last_m.sender == "system" and last_m.body == "job job-11: completed subagent — clean text"
)

-- 4.2: Suppress duplicate job completion entries when assistant repeats the result.
store._handle_line('{"kind":"job","event":"completed","job":"job-12","tool":"subagent","detail":"simulation finished successfully"}')
local job12_entry = recent_chat.messages[#recent_chat.messages]
check(
  "job completed entry has full detail before assistant speaks",
  job12_entry.body == "job job-12: completed subagent — simulation finished successfully"
)
-- Assistant speaks repeating the result:
store._handle_line('{"kind":"message","chat":"' .. recent_chat.id .. '","text":"Result: simulation finished successfully"}')
check(
  "job completed entry collapses when assistant repeats result",
  job12_entry.body == "job job-12: completed subagent"
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
