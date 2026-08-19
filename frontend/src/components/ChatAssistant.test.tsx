import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import ChatAssistant from './ChatAssistant'
import type { ChatMessage, Conversation } from '../types'

const mocks = vi.hoisted(() => ({
  fetchConversations: vi.fn(),
  createConversation: vi.fn(),
  fetchConversationMessages: vi.fn(),
  postChat: vi.fn(),
  subscribeWS: vi.fn(() => () => {}),
}))

vi.mock('../api/client', () => ({
  fetchConversations: mocks.fetchConversations,
  createConversation: mocks.createConversation,
  fetchConversationMessages: mocks.fetchConversationMessages,
  postChat: mocks.postChat,
}))
vi.mock('../api/ws', () => ({ subscribeWS: mocks.subscribeWS }))

const c1: Conversation = { id: 'c1', title: '会话一', created_at: '', updated_at: '' }
const c2: Conversation = { id: 'c2', title: '会话二', created_at: '', updated_at: '' }

beforeEach(() => {
  vi.clearAllMocks()
  mocks.fetchConversations.mockResolvedValue([c1])
  mocks.createConversation.mockResolvedValue({ id: 'c3', title: '', created_at: '', updated_at: '' })
  mocks.fetchConversationMessages.mockResolvedValue([])
  mocks.postChat.mockResolvedValue({ id: 'm9', role: 'assistant', content: 'ok', conversation_id: 'c1', created_at: '' })
})

describe('ChatAssistant', () => {
  it('初始收起，点击按钮展开/再点收起', async () => {
    render(<ChatAssistant />)
    expect(screen.queryByLabelText('对话历史')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: '智能助手' }))
    expect(await screen.findByLabelText('对话历史')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '智能助手' }))
    expect(screen.queryByLabelText('对话历史')).toBeNull()
  })

  it('加载最近会话并展示其消息', async () => {
    mocks.fetchConversationMessages.mockResolvedValue([
      { id: 'm1', role: 'user', content: '有哪些告警', conversation_id: 'c1', created_at: '' },
    ] as ChatMessage[])
    render(<ChatAssistant />)
    fireEvent.click(screen.getByLabelText('智能助手'))
    expect(await screen.findByText('有哪些告警')).toBeTruthy()
  })

  it('新对话创建并切换', async () => {
    render(<ChatAssistant />)
    fireEvent.click(screen.getByRole('button', { name: '智能助手' }))
    await waitFor(() => expect(mocks.fetchConversationMessages).toHaveBeenCalledWith('c1'))
    fireEvent.click(screen.getByText('新对话'))
    await waitFor(() => expect(mocks.createConversation).toHaveBeenCalled())
    fireEvent.change(screen.getByPlaceholderText('输入消息...'), { target: { value: 'hi' } })
    fireEvent.click(screen.getByText('发送'))
    await waitFor(() => expect(mocks.postChat).toHaveBeenCalledWith('c3', 'hi'))
  })

  it('切换会话加载对应消息', async () => {
    mocks.fetchConversations.mockResolvedValue([c1, c2])
    render(<ChatAssistant />)
    fireEvent.click(screen.getByLabelText('智能助手'))
    await waitFor(() => expect(mocks.fetchConversationMessages).toHaveBeenCalledWith('c1'))
    fireEvent.mouseDown(screen.getByRole('combobox'))
    fireEvent.click(await screen.findByText('会话二'))
    await waitFor(() => expect(mocks.fetchConversationMessages).toHaveBeenCalledWith('c2'))
  })

  it('发送携带当前 conversation_id', async () => {
    render(<ChatAssistant />)
    fireEvent.click(screen.getByLabelText('智能助手'))
    await waitFor(() => expect(mocks.fetchConversationMessages).toHaveBeenCalledWith('c1'))
    fireEvent.change(screen.getByPlaceholderText('输入消息...'), { target: { value: '你好' } })
    fireEvent.click(screen.getByText('发送'))
    await waitFor(() => expect(mocks.postChat).toHaveBeenCalledWith('c1', '你好'))
  })
})
