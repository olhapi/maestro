import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ComponentErrorBoundary } from '@/components/ui/component-error-boundary'

let shouldThrow = false

function ThrowingChild() {
  if (shouldThrow) {
    throw new Error('render failed')
  }

  return <div>Recovered child</div>
}

describe('ComponentErrorBoundary', () => {
  let consoleErrorSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    shouldThrow = false
    consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    consoleErrorSpy.mockRestore()
  })

  it('shows a fallback and reloads the failed subtree on demand', async () => {
    shouldThrow = true

    render(
      <ComponentErrorBoundary label="test widget" scope="widget">
        <ThrowingChild />
      </ComponentErrorBoundary>,
    )

    const reloadButton = await screen.findByRole('button', { name: /reload test widget/i })
    expect(screen.getByText(/couldn't render test widget/i)).toBeInTheDocument()

    shouldThrow = false
    fireEvent.click(reloadButton)

    expect(await screen.findByText('Recovered child')).toBeInTheDocument()
  })

  it('clears the fallback automatically when reset keys change', async () => {
    shouldThrow = true

    const { rerender } = render(
      <ComponentErrorBoundary label="test widget" resetKeys={['alpha']} scope="widget">
        <ThrowingChild />
      </ComponentErrorBoundary>,
    )

    expect(await screen.findByRole('button', { name: /reload test widget/i })).toBeInTheDocument()

    shouldThrow = false
    rerender(
      <ComponentErrorBoundary label="test widget" resetKeys={['beta']} scope="widget">
        <ThrowingChild />
      </ComponentErrorBoundary>,
    )

    expect(await screen.findByText('Recovered child')).toBeInTheDocument()
  })

  it('renders distinct page and widget fallbacks', async () => {
    shouldThrow = true

    const { rerender } = render(
      <ComponentErrorBoundary label="route page" scope="page">
        <ThrowingChild />
      </ComponentErrorBoundary>,
    )

    expect(await screen.findByText(/reload this page section/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /reload route page/i }).closest('[data-scope="page"]')).toHaveClass(
      'grid',
      'items-center',
      'justify-items-stretch',
    )

    rerender(
      <ComponentErrorBoundary label="chart widget" scope="widget">
        <ThrowingChild />
      </ComponentErrorBoundary>,
    )

    expect(await screen.findByText(/reload this component/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /reload chart widget/i }).closest('[data-scope="widget"]')).toHaveClass(
      'grid',
      'items-center',
      'justify-items-stretch',
    )
  })
})
