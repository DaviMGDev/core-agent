-- nvchat — shared state.
--
-- One place for everything the regions read and write: the listed sessions,
-- the open session, sidebar visibility, and the region buffers/windows.
-- Regions never talk to each other directly.

local M = {}

M.sessions = {} -- the launch's chats, as listed by the store
M.current = nil -- the open session
M.chat_visible = true -- chat column visibility (D21)
M.started = false -- ui.start() guard

M.jobs = {} -- the launch's job rows, as listed by the store
M.job_nodes = {} -- rendered job rows: { line, kind, row }
M.job_selected = nil -- the job under the jobs overlay cursor
M.jobs_cursor = nil -- the jobs overlay's remembered cursor
M.peek_errors = {} -- job id -> the core's refusal for a peek

M.bufs = {} -- region name -> buffer
M.wins = {} -- region name -> window (messages, composer)

M.message_blocks = {} -- rendered blocks of the open session: {first, last, message}

return M
