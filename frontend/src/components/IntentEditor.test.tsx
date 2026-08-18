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
    fireEvent.change(screen.getByLabelText(/示例/), { target: { value: '怎么查手册, 手册在哪' } })
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
})
