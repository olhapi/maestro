import { render, screen } from '@testing-library/react'

import { MarkdownText } from '@/components/ui/markdown'

describe('MarkdownText', () => {
  it('renders GFM content such as strikethrough, task lists, and tables', () => {
    render(
      <MarkdownText
        content={`Review the ~~draft~~ plan

- [x] Confirm scope
- [ ] Ship change

| Step | Status |
| --- | --- |
| Audit | done |`}
      />,
    )

    expect(screen.getByText('draft', { selector: 'del' })).toBeInTheDocument()

    const checkboxes = screen.getAllByRole('checkbox')
    expect(checkboxes).toHaveLength(2)
    expect(checkboxes[0]).toBeChecked()
    expect(checkboxes[1]).not.toBeChecked()

    expect(screen.getByRole('table')).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: 'Step' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'done' })).toBeInTheDocument()
  })

  it('wraps fenced code blocks instead of exposing horizontal scroll', () => {
    render(
      <MarkdownText
        content={`Run this command:

\`\`\`bash
npm run dev -- --filter=frontend --with-an-exceptionally-long-token-that-should-wrap
\`\`\``}
      />,
    )

    const codeBlock = screen.getByText(/exceptionally-long-token/, { selector: 'code' })
    const pre = codeBlock.closest('pre')

    expect(pre).toHaveClass('whitespace-pre-wrap', 'break-words')
    expect(pre).not.toHaveClass('overflow-x-auto')
  })
})
