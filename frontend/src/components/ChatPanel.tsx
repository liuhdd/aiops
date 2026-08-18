import { useState } from 'react'
import { Button, Card, Input, Space, Typography } from 'antd'
import { SendOutlined } from '@ant-design/icons'
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
    <Card title="智能助手" size="small" style={{ height: '100%' }}>
      <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
        <div style={{ flex: 1, overflowY: 'auto', minHeight: 200 }}>
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
    </Card>
  )
}
