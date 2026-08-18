import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '../test-utils'
import KnowledgeBase from './KnowledgeBase'
import { fetchKbDocs, fetchKbDoc, uploadKbDoc, deleteKbDoc } from '../api/client'
import type { KbDocSummary } from '../types'

vi.mock('../api/client', () => ({
  fetchKbDocs: vi.fn(),
  fetchKbDoc: vi.fn(),
  uploadKbDoc: vi.fn(),
  updateKbDoc: vi.fn(),
  deleteKbDoc: vi.fn(),
  fetchIntentLeaves: vi.fn().mockResolvedValue([]),
}))

const doc = (id: string, title: string, category: KbDocSummary['category']): KbDocSummary => ({
  id, title, category, device_types: ['switch'], vendor: ['H3C'], model: ['S5560'],
  metrics: [], keywords: ['光模块'], intents: [], source_file: '', content: '',
  created_at: '', updated_at: '', chunk_count: 2,
})

describe('KnowledgeBase', () => {
  it('渲染文档列表', async () => {
    vi.mocked(fetchKbDocs).mockResolvedValue([doc('d1', '交换机手册', 'manual')])
    render(<KnowledgeBase />)
    expect(await screen.findByText('交换机手册')).toBeTruthy()
    expect(screen.getByText(/2 块/)).toBeTruthy()
  })

  it('筛选变更调用 fetchKbDocs', async () => {
    vi.mocked(fetchKbDocs).mockResolvedValue([])
    render(<KnowledgeBase />)
    const q = await screen.findByPlaceholderText('关键词')
    fireEvent.change(q, { target: { value: '光模块' } })
    await waitFor(() => {
      expect(fetchKbDocs).toHaveBeenCalledWith(expect.objectContaining({ q: '光模块' }))
    })
  })

  it('上传成功后刷新列表', async () => {
    vi.mocked(fetchKbDocs).mockResolvedValue([])
    vi.mocked(uploadKbDoc).mockResolvedValue({} as never)
    const { container } = render(<KnowledgeBase />)
    const file = new File(['x'], 'a.md', { type: 'text/markdown' })
    const input = container.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    await act(async () => {})
    fireEvent.click(screen.getByText('上传'))
    await waitFor(() => expect(uploadKbDoc).toHaveBeenCalled())
  })

  it('删除经确认后调用 deleteKbDoc 并刷新', async () => {
    vi.mocked(fetchKbDocs).mockResolvedValue([doc('d1', '交换机手册', 'manual')])
    vi.mocked(deleteKbDoc).mockResolvedValue({ status: 'ok' })
    render(<KnowledgeBase />)
    await screen.findByText('交换机手册')
    fireEvent.click(screen.getByText('删除'))
    fireEvent.click(await screen.findByText('确定'))
    await waitFor(() => expect(deleteKbDoc).toHaveBeenCalledWith('d1'))
  })

  it('点击详情打开 Drawer', async () => {
    vi.mocked(fetchKbDocs).mockResolvedValue([doc('d1', '交换机手册', 'manual')])
    vi.mocked(fetchKbDoc).mockResolvedValue({
      ...doc('d1', '交换机手册', 'manual'),
      chunks: [{ id: 'c1', doc_id: 'd1', seq: 1, heading: '概述', content: '本手册介绍交换机', created_at: '' }],
    })
    render(<KnowledgeBase />)
    await screen.findByText('交换机手册')
    fireEvent.click(screen.getByText('详情'))
    expect(await screen.findByText('本手册介绍交换机')).toBeTruthy()
  })
})
