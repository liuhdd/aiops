import { useState } from 'react'
import type { IntentTreeNode } from '../types'

export function collectIds(node: IntentTreeNode): string[] {
  const ids = [node.id]
  for (const c of node.children) ids.push(...collectIds(c))
  return ids
}

const kindBadge: Record<string, string> = {
  GROUP: '分组', SYSTEM: '内置', KB: '知识库', MCP: 'MCP',
}

export default function IntentTree({
  nodes, onToggle, onDelete, onAddChild, onEdit, error,
}: {
  nodes: IntentTreeNode[]
  onToggle: (node: IntentTreeNode, ids: string[]) => void
  onDelete: (node: IntentTreeNode, ids: string[]) => void
  onAddChild: (node: IntentTreeNode) => void
  onEdit: (node: IntentTreeNode) => void
  error?: string
}) {
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})

  const renderNode = (node: IntentTreeNode, depth: number): JSX.Element => {
    const isGroup = node.kind === 'GROUP'
    const isCollapsed = collapsed[node.id]
    return (
      <div key={node.id}>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', padding: '4px 0', paddingLeft: depth * 20 }}>
          {isGroup && (
            <button onClick={() => setCollapsed((c) => ({ ...c, [node.id]: !c[node.id] }))}>
              {isCollapsed ? '+' : '-'}
            </button>
          )}
          <span style={{ fontWeight: isGroup ? 700 : 400 }}>{node.name}</span>
          <span style={{ color: '#888' }}>{kindBadge[node.kind]}</span>
          {!isGroup && <span style={{ color: '#888' }}>{node.code}</span>}
          <span style={{ color: node.enabled ? '#2e7d32' : '#999' }}>{node.enabled ? '启用' : '停用'}</span>
          <button onClick={() => onToggle(node, collectIds(node))}>{node.enabled ? '停用' : '启用'}</button>
          {isGroup && <button onClick={() => onAddChild(node)}>+子节点</button>}
          <button onClick={() => onEdit(node)}>编辑</button>
          <button onClick={() => onDelete(node, collectIds(node))}>删除</button>
        </div>
        {isGroup && !isCollapsed && node.children.map((c) => renderNode(c, depth + 1))}
      </div>
    )
  }

  return (
    <div>
      {error && <div style={{ color: '#d32f2f', padding: 8 }}>{error}</div>}
      {nodes.length === 0 && <div style={{ color: '#888', padding: 16 }}>暂无意图节点</div>}
      {nodes.map((n) => renderNode(n, 0))}
    </div>
  )
}
