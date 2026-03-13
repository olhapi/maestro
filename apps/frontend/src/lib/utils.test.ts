import { describe, expect, it, vi } from 'vitest'

import { formatCompactNumber, formatRelativeTimeCompact } from '@/lib/utils'

describe('formatCompactNumber', () => {
  it('renders large values using compact suffixes', () => {
    expect(formatCompactNumber(999)).toBe('999')
    expect(formatCompactNumber(10_000)).toBe('10K')
    expect(formatCompactNumber(100_000)).toBe('100K')
    expect(formatCompactNumber(1_200_000)).toBe('1.2M')
    expect(formatCompactNumber(1_000_000_000)).toBe('1B')
  })
})

describe('formatRelativeTimeCompact', () => {
  it('renders compact units for past timestamps', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-03-11T10:00:00Z'))

    expect(formatRelativeTimeCompact('2026-03-11T09:59:30Z')).toBe('30s ago')
    expect(formatRelativeTimeCompact('2026-03-11T09:55:00Z')).toBe('5m ago')
    expect(formatRelativeTimeCompact('2026-03-11T08:00:00Z')).toBe('2h ago')

    vi.useRealTimers()
  })
})
