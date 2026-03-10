import { describe, expect, it } from 'vitest'

import { formatCompactNumber } from '@/lib/utils'

describe('formatCompactNumber', () => {
  it('renders large values using compact suffixes', () => {
    expect(formatCompactNumber(999)).toBe('999')
    expect(formatCompactNumber(10_000)).toBe('10K')
    expect(formatCompactNumber(100_000)).toBe('100K')
    expect(formatCompactNumber(1_200_000)).toBe('1.2M')
    expect(formatCompactNumber(1_000_000_000)).toBe('1B')
  })
})
