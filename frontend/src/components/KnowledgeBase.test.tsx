import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import KnowledgeBase from './KnowledgeBase'
import { fetchKbDocs, uploadKbDoc, deleteKbDoc } from '../api/client'
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
    render(<KnowledgeBase />)
    const file = new File(['x'], 'a.md', { type: 'text/markdown' })
    const input = await screen.findByLabelText('上传文件')
    fireEvent.change(input, { target: { files: [file] } })
    fireEvent.click(screen.getByText('上传'))
    await waitFor(() => expect(uploadKbDoc).toHaveBeenCalled())
  })

  it('删除后调用 deleteKbDoc 并刷新', async () => {
    vi.mocked(fetchKbDocs).mockResolvedValue([doc('d1', '交换机手册', 'manual')])
    vi.mocked(deleteKbDoc).mockResolvedValue({ status: 'ok' })
    render(<KnowledgeBase />)
    await screen.findByText('交换机手册')
    fireEvent.click(screen.getByText('删除'))
    await waitFor(() => expect(deleteKbDoc).toHaveBeenCalledWith('d1'))
  })
})
