-- Run with: CSF_PARSER=/path/csf.so nvim --headless -u NONE -l ... (0.10+),
-- or nvim --headless -u NONE -c 'luafile ...' on 0.9.
local ok, message = pcall(function()
  local module_root = assert(vim.env.CSF_MODULE_ROOT)
  local parser_root = module_root .. '/csf/editor/tree-sitter-csf'
  vim.opt.runtimepath:append(parser_root .. '/neovim')
  vim.treesitter.language.add('csf', { path = assert(vim.env.CSF_PARSER) })
  vim.cmd('filetype plugin on')
  vim.cmd('runtime! ftdetect/csf.lua')
  vim.cmd('edit ' .. vim.fn.fnameescape(module_root .. '/csf/architecture/architecture.csf'))
  assert(vim.bo.filetype == 'csf', 'architecture filetype detection failed')
  local parser = vim.treesitter.get_parser(0, 'csf')
  local tree = parser:parse()[1]
  assert(not tree:root():has_error(), 'architecture did not parse in Neovim')
  assert(vim.treesitter.highlighter.active[vim.api.nvim_get_current_buf()], 'highlighter is inactive')
  local query = vim.treesitter.query.get('csf', 'highlights')
  local captures = {}
  for id in query:iter_captures(tree:root(), 0) do captures[query.captures[id]] = true end
  assert(captures.keyword and captures.string and captures.comment, 'missing actual highlight captures')
  vim.cmd('enew')
  vim.api.nvim_buf_set_lines(0, 0, -1, false, {'architecture// header', 'demo version 1 {}'})
  assert(vim.filetype.match({ filename = 'example.csf', buf = 0 }) == 'csf', 'comment token boundary was missed')
  vim.api.nvim_buf_set_lines(0, 0, -1, false, {'architecture', 'demo version 1 {}'})
  assert(vim.filetype.match({ filename = 'example.csf', buf = 0 }) == 'csf', 'newline token boundary was missed')
  vim.cmd('edit! ' .. vim.fn.fnameescape(module_root .. '/csf/compiler/language/architecture.csf'))
  assert(vim.bo.filetype ~= 'csf', 'documentation DSL incorrectly selects architecture parser')
end)
if not ok then
  io.stderr:write(tostring(message) .. '\n')
  vim.cmd('cquit 1')
else
  print('Neovim CSF parsing and highlighting passed')
  vim.cmd('qa!')
end
