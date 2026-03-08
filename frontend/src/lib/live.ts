export function connectDashboardSocket(onInvalidate: () => void) {
  let socket: WebSocket | null = null
  let retryTimer: number | null = null
  let closed = false

  const connect = () => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    socket = new WebSocket(`${protocol}//${window.location.host}/api/v1/ws`)
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
      if (closed) return
      retryTimer = window.setTimeout(connect, 1_500)
    }
    socket.onerror = () => {
      socket?.close()
    }
  }

  connect()

  return () => {
    closed = true
    if (retryTimer !== null) window.clearTimeout(retryTimer)
    socket?.close()
  }
}
