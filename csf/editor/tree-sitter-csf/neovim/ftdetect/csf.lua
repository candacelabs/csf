-- Architecture files and the separate documentation DSL both use .csf.
-- Select this parser only when the first non-comment token is architecture.
vim.filetype.add({
  extension = {
    csf = function(_, bufnr)
      for _, line in ipairs(vim.api.nvim_buf_get_lines(bufnr, 0, -1, false)) do
        local text = line:gsub('^%s+', '')
        if text ~= '' and not text:match('^//') then
          if text == 'architecture' or text:match('^architecture[%s{]') or text:match('^architecture//') then
            return 'csf'
          end
          return nil
        end
      end
    end,
  },
})
