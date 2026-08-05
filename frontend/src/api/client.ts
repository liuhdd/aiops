import type { Alert, AlertDetail, ChatMessage } from '../types'

const base = '/api'

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(base + path, init)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json() as Promise<T>
}

export const fetchAlerts = () => req<Alert[]>('/alerts')
export const fetchAlert = (id: string) => req<AlertDetail>(`/alerts/${id}`)
export const inspectAlert = (id: string) =>
  req<{ status: string; alert_id: string }>(`/alerts/${id}/inspect`, { method: 'POST' })
export const fetchMessages = () => req<ChatMessage[]>('/messages')
export const postChat = (text: string) =>
  req<ChatMessage>('/chat', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text }),
  })
export const wsUrl = () => {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  return `${proto}://${location.host}/ws`
}
