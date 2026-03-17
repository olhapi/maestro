import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { connectDashboardSocket } from '@/lib/live'

class MockWebSocket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3
  static instances: MockWebSocket[] = []

  readonly url: string
  readyState = MockWebSocket.CONNECTING
  onopen: (() => void) | null = null
  onmessage: ((event: MessageEvent<string>) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  close = vi.fn(() => {
    this.readyState = MockWebSocket.CLOSED
    this.onclose?.()
  })

  constructor(url: string) {
    this.url = url
    MockWebSocket.instances.push(this)
  }

  open() {
    this.readyState = MockWebSocket.OPEN
    this.onopen?.()
  }

  message(payload: unknown) {
    this.onmessage?.({ data: JSON.stringify(payload) } as MessageEvent<string>)
  }
}

describe('connectDashboardSocket', () => {
  const originalWebSocket = window.WebSocket
  const originalVisibilityState = Object.getOwnPropertyDescriptor(Document.prototype, 'visibilityState')

  beforeEach(() => {
    vi.useFakeTimers()
    MockWebSocket.instances = []
    Object.defineProperty(window, 'WebSocket', {
      configurable: true,
      writable: true,
      value: MockWebSocket,
    })
    Object.defineProperty(document, 'visibilityState', {
      configurable: true,
      get: () => 'visible',
    })
  })

  afterEach(() => {
    vi.useRealTimers()
    Object.defineProperty(window, 'WebSocket', {
      configurable: true,
      writable: true,
      value: originalWebSocket,
    })
    if (originalVisibilityState) {
      Object.defineProperty(Document.prototype, 'visibilityState', originalVisibilityState)
    }
  })

  it('reconnects and refreshes when the tab regains focus', () => {
    const onInvalidate = vi.fn()

    const handle = connectDashboardSocket({ onInvalidate })

    vi.advanceTimersByTime(500)
    expect(MockWebSocket.instances).toHaveLength(1)

    const initialSocket = MockWebSocket.instances[0]
    initialSocket.open()

    window.dispatchEvent(new Event('focus'))

    expect(onInvalidate).toHaveBeenCalledTimes(1)
    expect(initialSocket.close).toHaveBeenCalledTimes(1)
    expect(MockWebSocket.instances).toHaveLength(2)
    expect(MockWebSocket.instances[1].url).toContain('/api/v1/ws')

    handle.disconnect()
  })

  it('refreshes when the server sends invalidate events', () => {
    const onInvalidate = vi.fn()

    const handle = connectDashboardSocket({ onInvalidate })

    vi.advanceTimersByTime(500)
    const socket = MockWebSocket.instances[0]
    socket.message({ type: 'invalidate' })

    expect(onInvalidate).toHaveBeenCalledTimes(1)

    handle.disconnect()
  })
})
