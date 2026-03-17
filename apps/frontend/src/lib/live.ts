export type DashboardSocketStatus = 'connecting' | 'connected' | 'reconnecting'

type DashboardSocketOptions = {
  onInvalidate: () => void
  onSignal?: () => void
  onStatusChange?: (status: DashboardSocketStatus) => void
}

export type DashboardSocketHandle = {
  reconnect: () => void
  disconnect: () => void
}

export function connectDashboardSocket({ onInvalidate, onSignal, onStatusChange }: DashboardSocketOptions): DashboardSocketHandle {
  let socket: WebSocket | null = null
  let retryTimer: number | null = null
  let closed = false
  let retryDelay = 1_500
  let hasConnected = false
  let lastResumeRefreshAt = 0

  const updateStatus = (status: DashboardSocketStatus) => {
    onStatusChange?.(status)
  }

  const clearRetryTimer = () => {
    if (retryTimer === null) {
      return
    }
    window.clearTimeout(retryTimer)
    retryTimer = null
  }

  const disconnectSocket = () => {
    if (!socket) {
      return
    }
    socket.onopen = null
    socket.onmessage = null
    socket.onclose = null
    socket.onerror = null
    socket.close()
    socket = null
  }

  const scheduleReconnect = () => {
    if (closed || retryTimer !== null) return
    updateStatus('reconnecting')
    retryTimer = window.setTimeout(() => {
      retryTimer = null
      connect()
    }, retryDelay)
    retryDelay = Math.min(retryDelay * 2, 5_000)
  }

  const connect = () => {
    if (closed) {
      return
    }
    if (socket && (socket.readyState === WebSocket.CONNECTING || socket.readyState === WebSocket.OPEN)) {
      return
    }
    updateStatus(hasConnected ? 'reconnecting' : 'connecting')
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const nextSocket = new WebSocket(`${protocol}//${window.location.host}/api/v1/ws`)
    socket = nextSocket
    nextSocket.onopen = () => {
      hasConnected = true
      retryDelay = 1_500
      updateStatus('connected')
      onSignal?.()
    }
    nextSocket.onmessage = (event) => {
      try {
        const payload = JSON.parse(event.data) as { type?: string }
        if (payload.type === 'invalidate') {
          onInvalidate()
        }
      } catch {
        onInvalidate()
      }
    }
    nextSocket.onclose = () => {
      if (socket === nextSocket) {
        socket = null
      }
      scheduleReconnect()
    }
    nextSocket.onerror = () => {}
  }

  const reconnectNow = () => {
    if (closed) {
      return
    }
    clearRetryTimer()
    disconnectSocket()
    connect()
  }

  const refreshOnResume = () => {
    if (closed || document.visibilityState === 'hidden') {
      return
    }
    const now = Date.now()
    if (now - lastResumeRefreshAt < 250) {
      return
    }
    lastResumeRefreshAt = now
    onInvalidate()
    reconnectNow()
  }

  retryTimer = window.setTimeout(() => {
    retryTimer = null
    connect()
  }, 500)

  window.addEventListener('focus', refreshOnResume)
  document.addEventListener('visibilitychange', refreshOnResume)

  return {
    reconnect: reconnectNow,
    disconnect: () => {
      closed = true
      clearRetryTimer()
      window.removeEventListener('focus', refreshOnResume)
      document.removeEventListener('visibilitychange', refreshOnResume)
      disconnectSocket()
    },
  }
}
