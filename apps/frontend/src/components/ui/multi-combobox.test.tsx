import { screen } from '@testing-library/react'

import { MultiCombobox } from '@/components/ui/multi-combobox'
import { renderWithQueryClient } from '@/test/test-utils'

describe('MultiCombobox', () => {
  it('keeps the trigger pinned to the right when selections are rendered', () => {
    const { container } = renderWithQueryClient(
      <MultiCombobox
        ariaLabel="Blockers"
        emptyText="No blockers."
        onChange={() => undefined}
        options={[
          { value: 'TEST-1', label: 'TEST-1 · issue 1' },
          { value: 'TEST-2', label: 'TEST-2 · issue 2' },
        ]}
        placeholder="Select blocker issues"
        value={['TEST-1']}
      />,
    )

    expect(screen.getByText('TEST-1 · issue 1')).toBeInTheDocument()
    expect(screen.getByPlaceholderText('Add more…')).toBeInTheDocument()

    const trigger = container.querySelector('button[aria-hidden="true"]')
    expect(trigger).not.toBeNull()
    expect(trigger).toHaveClass('absolute', 'right-3', 'top-1/2', '-translate-y-1/2')
    expect(trigger?.parentElement).toHaveClass('relative')
    expect(trigger?.previousElementSibling).toHaveClass('pr-12')
  })
})
