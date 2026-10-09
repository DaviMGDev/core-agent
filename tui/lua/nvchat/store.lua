-- nvchat — the store.
--
-- The store abstraction is four operations: list the chats, open the recent
-- one, load a chat's turns, deliver a sent line. This module is the only Lua
-- code that knows the core exists: it spawns `core-agent -tui` per launch,
-- speaks the JSON-lines link over stdio, and serves the four operations from
-- launch-local memory. Chats are created lazily with ids minted here, named
-- by their first user line (or "Chat N"); the core has no create op and no
-- titles. Quitting discards everything (chat.pseudo, tui.pseudo).

local M = {}

-- error is set when the core is unreachable or the link died; the UI
-- shows it and freezes (recovery is relaunching nvchat).
M.error = nil

-- on_error is called once when the link dies, so the screen can freeze.
M.on_error = nil

-- The chat list, in creation order; each chat is
-- { id, name, messages = { {sender, time, body} }, loaded, loading }.
local chats = {}
local by_id = {}
local last = nil -- the chat last touched (the fresh launch's recent())
local seq = 0 -- id and "Chat N" counter, launch-local

local job = nil
local stopping = false
local stdout_buf = "" -- partial line carried across callbacks
local pending = {} -- chat id -> the last deliver's handlers

local function now()
  return os.date("%H:%M")
end

-- append adds one message to a chat and marks it the recent chat.
local function append(chat, sender, body)
  table.insert(chat.messages, { sender = sender, time = now(), body = body })
  last = chat
end

-- first_line(text) -> the text's first line, trimmed; used for chat names.
local function first_line(text)
  local line = vim.split(text, "\n", { plain = true })[1] or ""
  return vim.trim(line)
end

local function notify_error(reason)
  if not M.error then
    M.error = reason
    if M.on_error then
      -- The screen renders the failure in place and freezes; no popup, no
      -- hit-enter prompt on the way there.
      M.on_error(reason)
    else
      vim.notify("nvchat: " .. reason, vim.log.levels.ERROR)
    end
  end
end

-- handle_line serves one link message.
local function handle_line(line)
  if line == "" then
    return
  end
  local ok, msg = pcall(vim.json.decode, line)
  if not ok or type(msg) ~= "table" then
    return
  end

  if msg.kind == "message" then
    -- A reply lands in its chat; speech the store cannot place (a launch
    -- default the screen never used) still reaches the transcript.
    local chat = by_id[msg.chat] or last
    if chat then
      local message = { sender = "assistant", time = now(), body = msg.text }
      table.insert(chat.messages, message)
      last = chat
      local h = pending[chat.id]
      if h and h.on_reply then
        h.on_reply(chat, message)
      end
    end
  elseif msg.kind == "loaded" then
    local chat = by_id[msg.chat]
    if chat then
      -- The core's record answers only a chat with no local activity: a
      -- late answer must never wipe an echo or a reply that beat it.
      if #chat.messages == 0 then
        for _, turn in ipairs(msg.messages or {}) do
          table.insert(chat.messages, {
            sender = turn.role == "user" and "you" or "assistant",
            time = "",
            body = turn.text,
          })
        end
      end
      chat.loaded = true
      chat.loading = false
    end
  elseif msg.kind == "job" then
    -- Job state changes join the transcript as system entries (tui.pseudo).
    if last then
      local body = "job " .. (msg.job or "?") .. ": " .. (msg.event or "?")
      if msg.tool and msg.tool ~= "" then
        body = body .. " " .. msg.tool
      end
      if msg.detail and msg.detail ~= "" then
        body = body .. " — " .. msg.detail
      end
      append(last, "system", body)
      local h = pending[last.id]
      if h and h.on_reply then
        h.on_reply(last, last.messages[#last.messages])
      end
    end
  elseif msg.kind == "error" then
    -- A failed turn is refused, not fatal: the link stays up.
    vim.notify("nvchat: " .. (msg.error or "the core refused a request"), vim.log.levels.WARN)
  end
end

-- handle_stdout reassembles whole lines from job chunks.
local function handle_stdout(_, data)
  data[1] = stdout_buf .. data[1]
  stdout_buf = table.remove(data)
  for _, line in ipairs(data) do
    handle_line(line)
  end
end

-- ensure_core spawns the per-launch core on first use.
local function ensure_core()
  if job then
    return job
  end
  if M.error then
    return nil
  end
  local bin = vim.g.nvchat_core or "core-agent"
  local args = vim.g.nvchat_core_args or {}
  local cmd = { bin, "-tui" }
  vim.list_extend(cmd, args)
  local started, id = pcall(vim.fn.jobstart, cmd, {
    stdin = "pipe",
    on_stdout = handle_stdout,
    on_exit = function(_, code)
      if stopping then
        return
      end
      job = nil
      notify_error("core-agent exited (code " .. code .. ")")
    end,
  })
  if not started or id <= 0 then
    job = nil
    notify_error("core-agent did not start (" .. bin .. ")")
    return nil
  end
  job = id
  return job
end

-- send writes one request line to the link.
local function send(msg)
  if not ensure_core() then
    return false
  end
  local ok, err = pcall(vim.fn.chansend, job, vim.json.encode(msg) .. "\n")
  if not ok or not err or err == 0 then
    notify_error("the link to the core is closed")
    return false
  end
  return true
end

-- list() -> the chats of this launch, in creation order. A fresh launch
-- opens one blank chat.
function M.list()
  if not ensure_core() then
    return {}
  end
  if #chats == 0 then
    M.create()
  end
  return chats
end

-- create() -> a new blank chat, lazily; the core hears about it only when a
-- line is first delivered.
function M.create()
  seq = seq + 1
  local chat = {
    id = "chat-" .. seq,
    name = "Chat " .. seq,
    messages = {},
    loaded = false,
    loading = false,
  }
  table.insert(chats, chat)
  by_id[chat.id] = chat
  last = chat
  return chat
end

-- recent() -> the chat a fresh launch opens: the last touched, else the
-- first.
function M.recent()
  M.list()
  return last or chats[1]
end

-- load(chat) -> the chat's turns, fetched from the core on first use and
-- cached. Waits briefly for the answer; the mock-free world is local.
function M.load(chat)
  if not chat then
    return {}
  end
  if not chat.loaded and not chat.loading and not M.error then
    chat.loading = true
    send({ kind = "load", chat = chat.id })
    vim.wait(5000, function()
      return chat.loaded or M.error ~= nil
    end, 10)
    chat.loading = false
  end
  return chat.messages
end

-- deliver(chat, text, handlers) -> the locally echoed message.
--
-- The echo is synchronous (the writer sees it before any ack); the reply is
-- asynchronous and lands through handlers.on_reply without blocking the
-- editor. handlers.on_echo(message) is optional.
function M.deliver(chat, text, handlers)
  handlers = handlers or {}
  local message = { sender = "you", time = now(), body = text }
  table.insert(chat.messages, message)
  if not chat.name_set and first_line(text) ~= "" then
    chat.name = first_line(text)
    chat.name_set = true
  end
  if handlers.on_echo then
    handlers.on_echo(message)
  end
  pending[chat.id] = handlers
  last = chat
  send({ kind = "deliver", chat = chat.id, text = text })
  return message
end

-- job_id() -> the spawned core's job id, 0 before the core starts.
function M.job_id()
  return job or 0
end

-- stop() -> deliberately ends the link (tests; reloads).
function M.stop()
  stopping = true
  if job then
    vim.fn.jobstop(job)
    job = nil
  end
end

return M
