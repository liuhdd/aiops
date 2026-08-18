import { useState } from 'react'
import { Button, Form, Input, Modal, Select } from 'antd'
import type { IntentTreeNode, IntentKind, IntentUpsert } from '../types'

export function parseExamples(str: string): string[] {
  return str.split(/[,，\n]/).map((s) => s.trim()).filter(Boolean)
}
export function parseKeywords(str: string): string[] {
  return str.split(/[,，]/).map((s) => s.trim()).filter(Boolean)
}

export default function IntentEditor({
  open, initial, groups, onSubmit, onCancel,
}: {
  open: boolean
  initial?: IntentTreeNode
  groups: IntentTreeNode[]
  onSubmit: (v: IntentUpsert) => void
  onCancel: () => void
}) {
  const [form] = Form.useForm<IntentUpsert>()
  const [kind, setKind] = useState<IntentKind>(initial?.kind ?? 'GROUP')

  const submit = async () => {
    const values = await form.validateFields().catch(() => undefined)
    if (!values) return
    onSubmit({
      ...values,
      rule_keywords: Array.isArray(values.rule_keywords)
        ? values.rule_keywords
        : parseKeywords(values.rule_keywords ?? ''),
      examples: Array.isArray(values.examples)
        ? values.examples
        : parseExamples(values.examples ?? ''),
    })
  }

  return (
    <Modal
      open={open}
      title={initial ? '编辑节点' : '新建节点'}
      onOk={submit}
      onCancel={onCancel}
      okText="保存"
      cancelText="取消"
      destroyOnHidden
    >
      <Form
        form={form}
        layout="vertical"
        initialValues={{
          parent_id: initial?.parent_id ?? '',
          name: initial?.name ?? '',
          kind: initial?.kind ?? 'GROUP',
          code: initial?.code ?? '',
          bind_target: initial?.bind_target ?? '',
          rule_keywords: (initial?.rule_keywords ?? []).join(', '),
          description: initial?.description ?? '',
          examples: (initial?.examples ?? []).join(', '),
          sort_order: initial?.sort_order ?? 0,
        }}
      >
        <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]}>
          <Input />
        </Form.Item>
        <Form.Item name="kind" label="类型">
          <Select
            options={[
              { value: 'GROUP', label: '分组' },
              { value: 'SYSTEM', label: '内置' },
              { value: 'KB', label: '知识库' },
              { value: 'MCP', label: 'MCP工具' },
            ]}
            onChange={(v: IntentKind) => setKind(v)}
          />
        </Form.Item>
        {kind !== 'GROUP' && (
          <>
            <Form.Item name="code" label="code">
              <Input />
            </Form.Item>
            {kind === 'KB' && (
              <Form.Item name="bind_target" label="知识库ID">
                <Input />
              </Form.Item>
            )}
            {kind === 'MCP' && (
              <Form.Item name="bind_target" label="mcpToolId">
                <Input />
              </Form.Item>
            )}
            <Form.Item name="rule_keywords" label="关键词(逗号分隔)">
              <Input />
            </Form.Item>
            <Form.Item name="description" label="描述">
              <Input />
            </Form.Item>
            <Form.Item name="examples" label="示例(逗号分隔)">
              <Input />
            </Form.Item>
          </>
        )}
        <Form.Item name="sort_order" label="排序">
          <Input type="number" />
        </Form.Item>
        <Form.Item name="parent_id" label="父节点">
          <Select
            options={[
              { value: '', label: '（顶层）' },
              ...groups.map((g) => ({ value: g.id, label: g.name })),
            ]}
          />
        </Form.Item>
      </Form>
    </Modal>
  )
}
