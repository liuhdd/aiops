import type {
  Alert,
  AlertDetail,
  ChatMessage,
  IntentNode,
  IntentTreeNode,
  IntentUpsert,
} from '../types'

const base = '/api'

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(base + path, init)
  if (!res.ok) {
    let msg = `HTTP ${res.status}`
    try {
      const data = await res.json()
      if (data && typeof data.error === 'string') msg = data.error
    } catch {
      /* ignore */
    }
    throw new Error(msg)
  }
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

export const fetchIntentTree = () => req<IntentTreeNode[]>('/intents/tree')
export const createIntent = (body: IntentUpsert) =>
  req<IntentNode>('/intents', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
export const updateIntent = (id: string, body: IntentUpsert) =>
  req<IntentNode>(`/intents/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
export const deleteIntents = (ids: string[]) =>
  req<{ status: string }>('/intents', {
    method: 'DELETE',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids }),
  })
export const setIntentStatus = (ids: string[], enabled: boolean) =>
  req<{ status: string }>('/intents/status', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids, enabled }),
  })
