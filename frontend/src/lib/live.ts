export function connectDashboardSocket(onInvalidate: () => void) {
  let socket: WebSocket | null = null
  let retryTimer: number | null = null
  let closed = false
  let retryDelay = 1_500

  const scheduleReconnect = () => {
    if (closed || retryTimer !== null) return
    retryTimer = window.setTimeout(() => {
      retryTimer = null
      connect()
    }, retryDelay)
    retryDelay = Math.min(retryDelay * 2, 5_000)
  }

  const connect = () => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    socket = new WebSocket(`${protocol}//${window.location.host}/api/v1/ws`)
    socket.onopen = () => {
      retryDelay = 1_500
    }
    socket.onmessage = (event) => {
      try {
        const payload = JSON.parse(event.data) as { type?: string }
        if (payload.type === 'invalidate') {
          onInvalidate()
        }
      } catch {
        onInvalidate()
      }
    }
    socket.onclose = () => {
      scheduleReconnect()
    }
    socket.onerror = () => {}
  }

  retryTimer = window.setTimeout(() => {
    retryTimer = null
    connect()
  }, 500)

  return () => {
    closed = true
    if (retryTimer !== null) window.clearTimeout(retryTimer)
    socket?.close()
  }
}
