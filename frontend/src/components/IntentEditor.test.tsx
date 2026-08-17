import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import IntentEditor from './IntentEditor'

describe('IntentEditor', () => {
  it('提交时解析示例并携带 examples', () => {
    const onSubmit = vi.fn()
    render(<IntentEditor groups={[]} onSubmit={onSubmit} onCancel={vi.fn()} />)
    fireEvent.change(screen.getByLabelText(/类型/), { target: { value: 'KB' } })
    fireEvent.change(screen.getByLabelText(/示例/), { target: { value: '怎么查手册, 手册在哪' } })
    fireEvent.click(screen.getByText('保存'))
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ examples: ['怎么查手册', '手册在哪'] }),
    )
  })
})
