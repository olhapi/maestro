import { useEffect, useState } from 'react'

import { formatRelativeTimeCompact } from '@/lib/utils'

// Share one ticking interval across all relative-time labels to avoid per-instance timers.
const compactRelativeTimeListeners = new Set<() => void>()
let compactRelativeTimeInterval: number | null = null

function subscribeCompactRelativeTime(listener: () => void) {
  compactRelativeTimeListeners.add(listener)
  if (compactRelativeTimeInterval === null) {
    compactRelativeTimeInterval = window.setInterval(() => {
      for (const nextListener of compactRelativeTimeListeners) {
        nextListener()
      }
    }, 1000)
  }

  return () => {
    compactRelativeTimeListeners.delete(listener)
    if (compactRelativeTimeListeners.size === 0 && compactRelativeTimeInterval !== null) {
      window.clearInterval(compactRelativeTimeInterval)
      compactRelativeTimeInterval = null
    }
  }
}

export function CompactRelativeTime({ value }: { value?: string | null }) {
  const [nowMs, setNowMs] = useState(() => Date.now())

  useEffect(() => {
    if (!value) {
      return
    }

    const timestampMs = new Date(value).getTime()
    if (Number.isNaN(timestampMs)) {
      return
    }

    return subscribeCompactRelativeTime(() => {
      setNowMs(Date.now())
    })
  }, [value])

  return <>{formatRelativeTimeCompact(value, nowMs)}</>
}
