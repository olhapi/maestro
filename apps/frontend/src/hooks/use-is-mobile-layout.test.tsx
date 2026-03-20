import { render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { useIsMobileLayout } from '@/hooks/use-is-mobile-layout'

function Harness() {
  useIsMobileLayout()
  return null
}

describe('useIsMobileLayout', () => {
  it('subscribes and unsubscribes the viewport listeners', () => {
    const addEventListenerSpy = vi.spyOn(window, 'addEventListener')
    const removeEventListenerSpy = vi.spyOn(window, 'removeEventListener')

    const { unmount } = render(<Harness />)

    expect(addEventListenerSpy).toHaveBeenCalledWith('resize', expect.any(Function))
    expect(addEventListenerSpy).toHaveBeenCalledWith('orientationchange', expect.any(Function))

    unmount()

    expect(removeEventListenerSpy).toHaveBeenCalledWith('resize', expect.any(Function))
    expect(removeEventListenerSpy).toHaveBeenCalledWith('orientationchange', expect.any(Function))
  })
})
