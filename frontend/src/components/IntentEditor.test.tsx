import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '../test-utils'
import IntentEditor, { parseExamples, parseKeywords } from './IntentEditor'

describe('parseExamples / parseKeywords', () => {
  it('按中英文逗号/换行拆分并去空', () => {
    expect(parseExamples('怎么查手册, 手册在哪\n 第三个，第四')).toEqual(['怎么查手册', '手册在哪', '第三个', '第四'])
    expect(parseKeywords('温控,风扇, 电源')).toEqual(['温控', '风扇', '电源'])
  })
})

describe('IntentEditor', () => {
  it('切换类型为知识库后填写示例并提交', async () => {
    const onSubmit = vi.fn()
    render(<IntentEditor open groups={[]} onSubmit={onSubmit} onCancel={vi.fn()} />)
    fireEvent.mouseDown(screen.getByLabelText(/类型/))
    fireEvent.click(await screen.findByText('知识库'))
    fireEvent.change(screen.getByLabelText(/名称/), { target: { value: '测试意图' } })
    fireEvent.change(await screen.findByLabelText(/示例/), { target: { value: '怎么查手册, 手册在哪' } })
    fireEvent.click(screen.getByText('保存'))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ examples: ['怎么查手册', '手册在哪'], kind: 'KB' }),
    ))
  })

  it('名称必填校验阻止提交', async () => {
    const onSubmit = vi.fn()
    render(<IntentEditor open groups={[]} onSubmit={onSubmit} onCancel={vi.fn()} />)
    fireEvent.click(screen.getByText('保存'))
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('编辑非 GROUP 节点时渲染类型专属字段并保留数据', async () => {
    const onSubmit = vi.fn()
    const initial = {
      id: 'n1', parent_id: '', name: '查温度', kind: 'KB' as const, code: 'check_temp',
      bind_target: '', rule_keywords: ['温度'], description: '查询设备温度', examples: ['温度多少'],
      enabled: true, deleted: false, sort_order: 0, created_at: '', updated_at: '', children: [],
    }
    render(<IntentEditor open initial={initial} groups={[]} onSubmit={onSubmit} onCancel={vi.fn()} />)
    expect(screen.getByLabelText(/code/)).toBeTruthy()
    expect(screen.getByLabelText(/关键词/)).toBeTruthy()
    expect(screen.getByLabelText(/示例/)).toBeTruthy()
    fireEvent.change(screen.getByLabelText(/名称/), { target: { value: '查温度' } })
    fireEvent.click(screen.getByText('保存'))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ examples: ['温度多少'], rule_keywords: ['温度'], kind: 'KB' }),
    ))
  })

  it('sort_order 以数字提交', async () => {
    const onSubmit = vi.fn()
    render(<IntentEditor open groups={[]} onSubmit={onSubmit} onCancel={vi.fn()} />)
    fireEvent.change(screen.getByLabelText(/名称/), { target: { value: '分组A' } })
    fireEvent.change(screen.getByLabelText(/排序/), { target: { value: '3' } })
    fireEvent.click(screen.getByText('保存'))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ sort_order: 3 })))
  })
})
