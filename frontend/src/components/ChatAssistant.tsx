import { useEffect, useState } from 'react'
import { FloatButton } from 'antd'
import { MessageOutlined } from '@ant-design/icons'
import ChatPanel from './ChatPanel'
import { createConversation, fetchConversationMessages, fetchConversations, postChat } from '../api/client'
import { subscribeWS } from '../api/ws'
import type { ChatMessage, Conversation } from '../types'

export default function ChatAssistant() {
  const [open, setOpen] = useState(false)
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [currentId, setCurrentId] = useState<string | null>(null)
  const [messagesByConv, setMessagesByConv] = useState<Record<string, ChatMessage[]>>({})

  useEffect(() => {
    let cancelled = false
    const init = async () => {
      let list = await fetchConversations()
      if (list.length === 0) {
        const conv = await createConversation()
        list = [conv]
      }
      if (cancelled) return
      setConversations(list)
      const first = list[0]
      setCurrentId(first.id)
      const msgs = await fetchConversationMessages(first.id)
      if (!cancelled) setMessagesByConv((prev) => ({ ...prev, [first.id]: msgs }))
    }
    init().catch(() => {})
    const unsub = subscribeWS((m) => {
      if (m.type !== 'message') return
      const msg = m.data as ChatMessage
      if (!msg.conversation_id) return
      setMessagesByConv((prev) => {
        const cur = prev[msg.conversation_id!] ?? []
        if (cur.some((x) => x.id === msg.id)) return prev
        return { ...prev, [msg.conversation_id!]: [...cur, msg] }
      })
      fetchConversations().then(setConversations).catch(() => {})
    })
    return () => { cancelled = true; unsub() }
  }, [])

  const selectConversation = async (id: string) => {
    setCurrentId(id)
    if (!messagesByConv[id]) {
      const msgs = await fetchConversationMessages(id)
      setMessagesByConv((prev) => ({ ...prev, [id]: msgs }))
    }
  }

  const newConversation = async () => {
    const conv = await createConversation()
    setConversations((prev) => [conv, ...prev])
    setCurrentId(conv.id)
    setMessagesByConv((prev) => ({ ...prev, [conv.id]: [] }))
  }

  const handleSend = async (text: string) => {
    const reply = await postChat(currentId ?? '', text)
    if (currentId) {
      const msgs = await fetchConversationMessages(currentId)
      setMessagesByConv((prev) => ({ ...prev, [currentId]: msgs }))
    }
    return reply
  }

  return (
    <>
      <FloatButton
        icon={<MessageOutlined />}
        type="primary"
        style={{ right: 24, bottom: 24 }}
        onClick={() => setOpen((o) => !o)}
        aria-label="智能助手"
      />
      {open && currentId && (
        <div
          role="dialog"
          aria-label="智能助手"
          style={{
            position: 'fixed',
            right: 24,
            bottom: 88,
            width: 380,
            height: 560,
            display: 'flex',
            flexDirection: 'column',
            background: '#1c2333',
            borderRadius: 8,
            boxShadow: '0 4px 16px rgba(0,0,0,0.4)',
            overflow: 'hidden',
            padding: 12,
          }}
        >
          <ChatPanel
            messages={messagesByConv[currentId] ?? []}
            onSend={handleSend}
            conversations={conversations}
            currentId={currentId}
            onSelectConversation={selectConversation}
            onNewConversation={newConversation}
            onClose={() => setOpen(false)}
          />
        </div>
      )}
    </>
  )
}
