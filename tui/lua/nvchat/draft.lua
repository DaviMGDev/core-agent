-- nvchat — unsent draft persistence.
--
-- Drafts are client-local state, not part of the session store (D18): one
-- JSON map keyed by session id, under the nvchat appname's state directory.
-- The map is rewritten at the save points — quitting, switching sessions,
-- sending — and read when a session opens.

local M = {}

local function path()
  return vim.fn.stdpath("state") .. "/drafts.json"
end

local function read_all()
  local ok, lines = pcall(vim.fn.readfile, path())
  if not ok or #lines == 0 then
    return {}
  end
  local decoded_ok, decoded = pcall(vim.json.decode, table.concat(lines, "\n"))
  if not decoded_ok or type(decoded) ~= "table" then
    return {}
  end
  return decoded
end

-- get(session) -> the saved draft for one session, or "".
function M.get(session)
  if not session then
    return ""
  end
  return read_all()[session.id] or ""
end

-- put(session, text) -> saves one session's draft; an empty draft is
-- forgotten rather than stored.
function M.put(session, text)
  if not session then
    return
  end
  local all = read_all()
  if text == nil or text == "" then
    all[session.id] = nil
  else
    all[session.id] = text
  end
  local dir = vim.fn.stdpath("state")
  vim.fn.mkdir(dir, "p")
  vim.fn.writefile({ vim.json.encode(all) }, path())
end

return M
