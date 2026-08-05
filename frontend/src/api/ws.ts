import type { WSMessage } from '../types'
import { wsUrl } from './client'

export function subscribeWS(onMessage: (m: WSMessage) => void): () => void {
  let closed = false
  let ws: WebSocket | null = null

  function connect() {
    ws = new WebSocket(wsUrl())
    ws.onmessage = (e) => {
      try {
        onMessage(JSON.parse(e.data) as WSMessage)
      } catch {
        /* ignore bad frames */
      }
    }
    ws.onclose = () => {
      if (!closed) setTimeout(connect, 3000)
    }
  }
  connect()
  return () => {
    closed = true
    ws?.close()
  }
}
