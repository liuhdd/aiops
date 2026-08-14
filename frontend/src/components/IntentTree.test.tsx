import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import IntentTree, { collectIds } from './IntentTree'
import type { IntentTreeNode } from '../types'

const leaf = (id: string, name: string): IntentTreeNode => ({
  id, parent_id: 'g', name, kind: 'KB', code: name, bind_target: '', rule_keywords: [],
  description: '', enabled: true, deleted: false, sort_order: 0, created_at: '', updated_at: '', children: [],
})

const group: IntentTreeNode = {
  id: 'g', parent_id: '', name: '分组', kind: 'GROUP', code: '', bind_target: '', rule_keywords: [],
  description: '', enabled: true, deleted: false, sort_order: 0, created_at: '', updated_at: '',
  children: [leaf('a', '意图A'), leaf('b', '意图B')],
}

describe('IntentTree', () => {
  it('collectIds 收集整棵子树', () => {
    expect(collectIds(group).sort()).toEqual(['a', 'b', 'g'].sort())
  })

  it('切换分组提交整棵子树 id', () => {
    const onToggle = vi.fn()
    render(<IntentTree nodes={[group]} onToggle={onToggle} onDelete={vi.fn()} onAddChild={vi.fn()} onEdit={vi.fn()} />)
    fireEvent.click(screen.getAllByText('停用')[0])
    expect(onToggle).toHaveBeenCalledWith(group, ['g', 'a', 'b'])
  })

  it('显示错误信息', () => {
    render(<IntentTree nodes={[group]} onToggle={vi.fn()} onDelete={vi.fn()} onAddChild={vi.fn()} onEdit={vi.fn()}
      error="子树不完整：节点 分组(g) 的后代 [a] 未包含" />)
    expect(screen.getByText(/子树不完整/)).toBeTruthy()
  })
})
