-- nvchat — rendering helpers.
--
-- Rendered regions are plain text in non-modifiable buffers. Writes go
-- through here so the modifiable toggle lives in one place.

local M = {}

function M.set_lines(buf, lines)
  vim.bo[buf].modifiable = true
  vim.api.nvim_buf_set_lines(buf, 0, -1, false, lines)
  vim.bo[buf].modifiable = false
  vim.bo[buf].modified = false
end

return M
