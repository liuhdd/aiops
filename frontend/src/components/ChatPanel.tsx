import { useState } from 'react'
import type { ChatMessage } from '../types'

export default function ChatPanel({
  messages,
  onSend,
}: {
  messages: ChatMessage[]
  onSend: (text: string) => Promise<ChatMessage>
}) {
  const [text, setText] = useState('')
  const [sending, setSending] = useState(false)

  const submit = async () => {
    if (!text.trim() || sending) return
    setSending(true)
    try {
      await onSend(text.trim())
      setText('')
    } finally {
      setSending(false)
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={{ flex: 1, overflowY: 'auto', padding: 8 }}>
        {messages.length === 0 && <div style={{ color: '#888' }}>开始对话，例如：查一下 alarm-100 什么原因</div>}
        {messages.map((m) => (
          <div key={m.id} style={{ textAlign: m.role === 'user' ? 'right' : 'left', margin: '6px 0' }}>
            <span style={{
              display: 'inline-block', maxWidth: '80%', padding: '6px 10px', borderRadius: 8,
              background: m.role === 'user' ? '#1976d2' : '#e0e0e0',
              color: m.role === 'user' ? '#fff' : '#111', whiteSpace: 'pre-wrap',
            }}>
              {m.content}
            </span>
          </div>
        ))}
      </div>
      <div style={{ display: 'flex', gap: 8, padding: 8 }}>
        <input
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && submit()}
          placeholder="输入消息..."
          style={{ flex: 1, padding: 8 }}
        />
        <button onClick={submit} disabled={sending}>发送</button>
      </div>
    </div>
  )
}
