import { Alert, Button, ConfigProvider, Popconfirm, Space, Tag, Tree } from 'antd'
import type { TreeDataNode } from 'antd'
import type { IntentTreeNode } from '../types'

export function collectIds(node: IntentTreeNode): string[] {
  const ids = [node.id]
  for (const c of node.children) ids.push(...collectIds(c))
  return ids
}

const kindBadge: Record<string, string> = {
  GROUP: '分组', SYSTEM: '内置', KB: '知识库', MCP: 'MCP',
}
const kindColor: Record<string, string> = {
  GROUP: 'geekblue', SYSTEM: 'purple', KB: 'cyan', MCP: 'orange',
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
  const toData = (list: IntentTreeNode[]): TreeDataNode[] =>
    list.map((n) => {
      const isGroup = n.kind === 'GROUP'
      return {
        key: n.id,
        title: (
          <Space size={8} wrap>
            <span style={{ fontWeight: isGroup ? 600 : 400 }}>{n.name}</span>
            <Tag color={kindColor[n.kind]} style={{ marginInlineEnd: 0 }}>{kindBadge[n.kind]}</Tag>
            {!isGroup && <span style={{ color: 'rgba(255,255,255,0.45)' }}>{n.code}</span>}
            <Tag color={n.enabled ? 'success' : 'default'} style={{ marginInlineEnd: 0 }}>
              {n.enabled ? '已启用' : '已停用'}
            </Tag>
            <Button size="small" onClick={() => onToggle(n, collectIds(n))}>
              {n.enabled ? '停用' : '启用'}
            </Button>
            {isGroup && <Button size="small" onClick={() => onAddChild(n)}>+子节点</Button>}
            <Button size="small" onClick={() => onEdit(n)}>编辑</Button>
            <Popconfirm
              title="确认删除？"
              okText="确定"
              onConfirm={() => onDelete(n, collectIds(n))}
            >
              <Button size="small" danger>删除</Button>
            </Popconfirm>
          </Space>
        ),
        children: n.children.length ? toData(n.children) : undefined,
      }
    })

  return (
    <ConfigProvider button={{ autoInsertSpace: false }}>
      <div>
        {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 8 }} />}
        {nodes.length === 0 ? (
          <div style={{ color: 'rgba(255,255,255,0.45)', padding: 16 }}>暂无意图节点</div>
        ) : (
          <Tree treeData={toData(nodes)} defaultExpandAll showLine blockNode />
        )}
      </div>
    </ConfigProvider>
  )
}
