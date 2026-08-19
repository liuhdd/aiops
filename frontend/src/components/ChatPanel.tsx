import { useEffect, useRef, useState } from 'react'
import { Button, Input, Select, Space, Typography } from 'antd'
import { CloseOutlined, PlusOutlined, SendOutlined } from '@ant-design/icons'
import type { ChatMessage, Conversation } from '../types'

export default function ChatPanel({
  messages,
  onSend,
  onNewConversation,
  onSelectConversation,
  conversations,
  currentId,
  onClose,
}: {
  messages: ChatMessage[]
  onSend: (text: string) => Promise<ChatMessage>
  onNewConversation: () => void
  onSelectConversation: (id: string) => void
  conversations: Conversation[]
  currentId: string
  onClose: () => void
}) {
  const [text, setText] = useState('')
  const [sending, setSending] = useState(false)
  const scrollRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [messages])

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
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, paddingBottom: 8 }}>
        <Typography.Text strong style={{ whiteSpace: 'nowrap' }}>智能助手</Typography.Text>
        <Select
          aria-label="会话切换"
          value={currentId}
          onChange={(v) => onSelectConversation(v)}
          style={{ flex: 1, minWidth: 0 }}
          options={conversations.map((c) => ({ value: c.id, label: c.title || '未命名对话' }))}
          placeholder="选择会话"
          popupMatchSelectWidth={false}
        />
        <Button size="small" icon={<PlusOutlined />} onClick={onNewConversation}>
          新对话
        </Button>
        <Button size="small" type="text" icon={<CloseOutlined />} aria-label="收起助手" onClick={onClose} />
      </div>
      <div
        ref={scrollRef}
        aria-label="对话历史"
        style={{ flex: 1, overflowY: 'auto', minHeight: 0, borderTop: '1px solid rgba(255,255,255,0.08)' }}
      >
        {messages.length === 0 && (
          <Typography.Text type="secondary">开始对话，例如：查一下 alarm-100 什么原因</Typography.Text>
        )}
        {messages.map((m) => (
          <div
            key={m.id}
            style={{
              display: 'flex',
              justifyContent: m.role === 'user' ? 'flex-end' : 'flex-start',
              margin: '6px 0',
            }}
          >
            <div className={`chat-bubble${m.role === 'user' ? ' chat-bubble-user' : ''}`}>
              {m.content}
            </div>
          </div>
        ))}
      </div>
      <Space.Compact style={{ width: '100%', marginTop: 8 }}>
        <Input.TextArea
          value={text}
          onChange={(e) => setText(e.target.value)}
          onPressEnter={(e) => {
            if (!e.shiftKey) {
              e.preventDefault()
              submit()
            }
          }}
          placeholder="输入消息..."
          autoSize={{ minRows: 1, maxRows: 4 }}
          aria-label="输入消息"
        />
        <Button type="primary" icon={<SendOutlined />} onClick={submit} loading={sending}>
          发送
        </Button>
      </Space.Compact>
    </div>
  )
}