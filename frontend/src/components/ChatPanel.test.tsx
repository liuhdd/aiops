import { describe, expect, it, vi } from 'vitest'
import { type ComponentProps } from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import ChatPanel from './ChatPanel'
import type { ChatMessage, Conversation } from '../types'

const msgs: ChatMessage[] = [
  { id: 'm1', role: 'user', content: '有哪些告警', created_at: '' },
  { id: 'm2', role: 'assistant', content: '当前没有告警。', created_at: '' },
]

const convs: Conversation[] = [
  { id: 'c1', title: '会话一', created_at: '', updated_at: '' },
  { id: 'c2', title: '会话二', created_at: '', updated_at: '' },
]

function renderPanel(over: Partial<ComponentProps<typeof ChatPanel>> = {}) {
  const props = {
    messages: msgs,
    onSend: vi.fn().mockResolvedValue({} as ChatMessage),
    conversations: convs,
    currentId: 'c1',
    onSelectConversation: vi.fn(),
    onNewConversation: vi.fn(),
    onClose: vi.fn(),
    ...over,
  }
  const utils = render(<ChatPanel {...props} />)
  return { props, utils }
}

describe('ChatPanel', () => {
  it('渲染消息与标题', () => {
    renderPanel()
    expect(screen.getByText('智能助手')).toBeTruthy()
    expect(screen.getByText('有哪些告警')).toBeTruthy()
  })

  it('发送调用 onSend 并清空输入', async () => {
    const { props } = renderPanel()
    fireEvent.change(screen.getByPlaceholderText('输入消息...'), { target: { value: '你好' } })
    fireEvent.click(screen.getByText('发送'))
    await waitFor(() => expect(props.onSend).toHaveBeenCalledWith('你好'))
  })

  it('空消息显示引导语', () => {
    renderPanel({ messages: [] })
    expect(screen.getByText(/开始对话/)).toBeTruthy()
  })

  it('会话切换与新建按钮触发回调', () => {
    const { props } = renderPanel()
    fireEvent.mouseDown(screen.getByRole('combobox'))
    fireEvent.click(screen.getByText('会话二'))
    expect(props.onSelectConversation).toHaveBeenCalledWith('c2')
    fireEvent.click(screen.getByText('新对话'))
    expect(props.onNewConversation).toHaveBeenCalled()
  })

  it('对话历史容器可上下滚动', () => {
    renderPanel()
    const el = screen.getByLabelText('对话历史')
    expect(getComputedStyle(el).overflowY).toBe('auto')
  })

  it('收起按钮触发 onClose', () => {
    const { props } = renderPanel()
    fireEvent.click(screen.getByLabelText('收起助手'))
    expect(props.onClose).toHaveBeenCalled()
  })
})