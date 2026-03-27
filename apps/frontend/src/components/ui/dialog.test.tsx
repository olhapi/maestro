import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'

describe('DialogContent', () => {
  it('keeps the close icon from shrinking', () => {
    render(
      <Dialog open onOpenChange={() => {}}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Example dialog</DialogTitle>
            <DialogDescription>Example description</DialogDescription>
          </DialogHeader>
          <p>Dialog body</p>
        </DialogContent>
      </Dialog>,
    )

    const dialog = screen.getByRole('dialog', { name: 'Example dialog' })
    const closeButton = within(dialog).getByRole('button')
    const closeIcon = closeButton.querySelector('svg')

    expect(closeIcon).not.toBeNull()
    expect(closeIcon).toHaveClass('shrink-0')
  })
})
