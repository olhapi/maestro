import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { PlanApprovalDocument } from '@/components/dashboard/plan-approval-review'

describe('PlanApprovalDocument', () => {
  it('keeps fenced code blocks intact while splitting structured sections', () => {
    render(
      <PlanApprovalDocument
        markdown={`Summary:
Keep the rollout small.

Plan:
\`\`\`yaml
questions: literal value
plan: literal value
tests: literal value
\`\`\`

Tests:
- Run the focused suite
`}
      />,
    )

    expect(screen.getByText('Review summary')).toBeInTheDocument()
    expect(screen.getByText('Implementation plan')).toBeInTheDocument()
    expect(screen.getByText('Test plan')).toBeInTheDocument()

    const codeBlock = screen.getByText(
      (content, element) => element?.tagName === 'CODE' && content.includes('questions: literal value'),
    ).closest('pre')
    expect(codeBlock).not.toBeNull()

    const renderedCodeBlock = codeBlock as HTMLElement
    expect(renderedCodeBlock).toHaveTextContent('plan: literal value')
    expect(renderedCodeBlock).toHaveTextContent('tests: literal value')
  })

  it('renders fallback plan content without repeating the dialog title', () => {
    render(<PlanApprovalDocument markdown={'Keep the rollout small.'} />)

    expect(screen.getByText('Proposed plan')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Review the proposed plan' })).not.toBeInTheDocument()
    expect(screen.getByText('Keep the rollout small.')).toBeInTheDocument()
  })
})
