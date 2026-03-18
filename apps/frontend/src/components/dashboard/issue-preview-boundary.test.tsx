import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { IssuePreviewBoundary } from '@/components/dashboard/issue-preview-boundary'
import { makeBootstrapResponse, makeIssueSummary } from '@/test/fixtures'

let shouldThrowPreview = false

vi.mock('@/components/dashboard/issue-preview-sheet', () => ({
  IssuePreviewSheet: ({ open }: { open: boolean }) => {
    if (open && shouldThrowPreview) {
      throw new Error('preview crashed')
    }

    return <div>{open ? 'Issue preview ready' : 'Issue preview closed'}</div>
  },
}))

describe('IssuePreviewBoundary', () => {
  let consoleErrorSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    shouldThrowPreview = false
    consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    consoleErrorSpy.mockRestore()
  })

  it('contains issue preview crashes and recovers the sheet subtree', async () => {
    shouldThrowPreview = true

    render(
      <div>
        <div>Route content</div>
        <IssuePreviewBoundary
          bootstrap={makeBootstrapResponse()}
          issue={makeIssueSummary()}
          onInvalidate={vi.fn().mockResolvedValue(undefined)}
          onOpenChange={vi.fn()}
          open
        />
      </div>,
    )

    const reloadButton = await screen.findByRole('button', { name: /reload issue preview/i })
    expect(screen.getByText('Route content')).toBeInTheDocument()

    shouldThrowPreview = false
    fireEvent.click(reloadButton)

    expect(await screen.findByText('Issue preview ready')).toBeInTheDocument()
    expect(screen.getByText('Route content')).toBeInTheDocument()
  })
})
