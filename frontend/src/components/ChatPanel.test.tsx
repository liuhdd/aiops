import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import ChatPanel from './ChatPanel'
import type { ChatMessage } from '../types'

const msgs: ChatMessage[] = [
  { id: 'm1', role: 'user', content: '有哪些告警', created_at: '' },
  { id: 'm2', role: 'assistant', content: '当前没有告警。', created_at: '' },
]

describe('ChatPanel', () => {
  it('渲染消息并发送', async () => {
    const onSend = vi.fn().mockResolvedValue({
      id: 'm3', role: 'assistant', content: 'ok', created_at: '',
    } as ChatMessage)
    render(<ChatPanel messages={msgs} onSend={onSend} />)
    expect(screen.getByText('智能助手')).toBeTruthy()
    expect(screen.getByText('有哪些告警')).toBeTruthy()
    fireEvent.change(screen.getByPlaceholderText('输入消息...'), { target: { value: '你好' } })
    fireEvent.click(screen.getByText('发送'))
    await waitFor(() => expect(onSend).toHaveBeenCalledWith('你好'))
  })

  it('空消息显示引导语', () => {
    render(<ChatPanel messages={[]} onSend={vi.fn()} />)
    expect(screen.getByText(/开始对话/)).toBeTruthy()
  })
})
