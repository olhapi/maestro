import { useEffect, useState } from 'react'

import { formatRelativeTimeCompact } from '@/lib/utils'

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

    const interval = window.setInterval(() => {
      setNowMs(Date.now())
    }, 1000)

    return () => {
      window.clearInterval(interval)
    }
  }, [value])

  return <>{formatRelativeTimeCompact(value, nowMs)}</>
}
