-- Exercise default package discovery, without manually adding runtime paths.
local ok, message = pcall(function()
  vim.cmd('filetype plugin on')
  vim.cmd('edit ' .. vim.fn.fnameescape(assert(vim.env.CSF_MODULE_ROOT) .. '/csf/architecture/architecture.csf'))
  assert(vim.bo.filetype == 'csf', 'installed CSF filetype was not discovered')
  local tree = vim.treesitter.get_parser(0, 'csf'):parse()[1]
  assert(not tree:root():has_error(), 'installed parser rejected the architecture')
  assert(vim.treesitter.highlighter.active[vim.api.nvim_get_current_buf()], 'installed highlighter is inactive')
  local query = vim.treesitter.query.get('csf', 'highlights')
  assert(query and #query.captures > 0, 'installed query was not discovered')
end)
if not ok then
  io.stderr:write(tostring(message) .. '\n')
  vim.cmd('cquit 1')
else
  print('Single CSF installation: automatic Neovim highlighting passed')
  vim.cmd('qa!')
end
