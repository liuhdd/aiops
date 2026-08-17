import type {
  Alert,
  AlertDetail,
  ChatMessage,
  IntentLeaf,
  IntentNode,
  IntentTreeNode,
  IntentUpsert,
  KbDoc,
  KbDocDetail,
  KbDocSummary,
  KbDocUpsert,
  KbFilter,
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
export const fetchIntentLeaves = () => req<IntentLeaf[]>('/intents/leaves')

export const fetchKbDocs = (f: KbFilter = {}) => {
  const qs = new URLSearchParams()
  if (f.category) qs.set('category', f.category)
  if (f.device_type) qs.set('device_type', f.device_type)
  if (f.vendor) qs.set('vendor', f.vendor)
  if (f.model) qs.set('model', f.model)
  if (f.intent) qs.set('intent', f.intent)
  if (f.q) qs.set('q', f.q)
  const s = qs.toString()
  return req<KbDocSummary[]>(`/kb/docs${s ? `?${s}` : ''}`)
}
export const fetchKbDoc = (id: string) => req<KbDocDetail>(`/kb/docs/${id}`)
export const uploadKbDoc = (form: FormData) =>
  req<KbDoc>('/kb/upload', { method: 'POST', body: form })
export const updateKbDoc = (id: string, body: KbDocUpsert) =>
  req<KbDoc>(`/kb/docs/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
export const deleteKbDoc = (id: string) =>
  req<{ status: string }>(`/kb/docs/${id}`, { method: 'DELETE' })
