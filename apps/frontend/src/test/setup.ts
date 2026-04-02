import * as matchers from '@testing-library/jest-dom/matchers'
import { afterEach, beforeEach, expect, vi } from 'vitest'

expect.extend(matchers)

type ConsoleCall = {
  level: 'error' | 'warn'
  args: unknown[]
}

class ResizeObserverMock {
  observe() {}

  unobserve() {}

  disconnect() {}
}

class IntersectionObserverMock {
  observe() {}

  unobserve() {}

  disconnect() {}

  takeRecords() {
    return []
  }
}

let unexpectedConsoleCalls: ConsoleCall[] = []

function formatConsoleArg(arg: unknown) {
  if (typeof arg === 'string') {
    return arg
  }
  try {
    return JSON.stringify(arg)
  } catch {
    return String(arg)
  }
}

beforeEach(() => {
  unexpectedConsoleCalls = []

  vi.stubGlobal('ResizeObserver', ResizeObserverMock)
  vi.stubGlobal('IntersectionObserver', IntersectionObserverMock)
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
    return window.setTimeout(() => callback(0), 0)
  })
  vi.stubGlobal('cancelAnimationFrame', (animationFrameID: number) => {
    window.clearTimeout(animationFrameID)
  })

  vi.spyOn(console, 'error').mockImplementation((...args) => {
    unexpectedConsoleCalls.push({ level: 'error', args })
  })
  vi.spyOn(console, 'warn').mockImplementation((...args) => {
    unexpectedConsoleCalls.push({ level: 'warn', args })
  })
})

afterEach(() => {
  const calls = unexpectedConsoleCalls.slice()
  unexpectedConsoleCalls = []
  vi.restoreAllMocks()
  vi.unstubAllGlobals()

  if (calls.length === 0) {
    return
  }

  throw new Error(
    `Unexpected console output:\n${calls
      .map(({ level, args }) => `[console.${level}] ${args.map(formatConsoleArg).join(' ')}`)
      .join('\n')}`,
  )
})

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false
}

if (!Element.prototype.setPointerCapture) {
  Element.prototype.setPointerCapture = () => {}
}

if (!Element.prototype.releasePointerCapture) {
  Element.prototype.releasePointerCapture = () => {}
}

if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {}
}

// Radix dialog scroll restoration hits jsdom's unimplemented scrollTo during open/close cycles.
try {
  Object.defineProperty(window, 'scrollTo', {
    configurable: true,
    value: () => {},
  })
} catch {
  window.scrollTo = () => {}
}
