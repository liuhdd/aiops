import { useState } from 'react'
import type { IntentTreeNode, IntentKind, IntentUpsert } from '../types'

export default function IntentEditor({
  initial, groups, onSubmit, onCancel,
}: {
  initial?: IntentTreeNode
  groups: IntentTreeNode[]
  onSubmit: (v: IntentUpsert) => void
  onCancel: () => void
}) {
  const [name, setName] = useState(initial?.name ?? '')
  const [kind, setKind] = useState<IntentKind>(initial?.kind ?? 'GROUP')
  const [code, setCode] = useState(initial?.code ?? '')
  const [bind, setBind] = useState(initial?.bind_target ?? '')
  const [kw, setKw] = useState((initial?.rule_keywords ?? []).join(', '))
  const [desc, setDesc] = useState(initial?.description ?? '')
  const [ex, setEx] = useState((initial?.examples ?? []).join(', '))
  const [sort, setSort] = useState(String(initial?.sort_order ?? 0))
  const [parent, setParent] = useState(initial?.parent_id ?? '')

  const submit = () => {
    const examples = ex.split(/[,，\n]/).map((s) => s.trim()).filter(Boolean)
    onSubmit({
      parent_id: parent, name, kind, code, bind_target: bind,
      rule_keywords: kw.split(/[,，]/).map((s) => s.trim()).filter(Boolean),
      description: desc, examples, sort_order: Number(sort) || 0,
    })
  }

  const input = { padding: 4, margin: '4px 0' }

  return (
    <div style={{ border: '1px solid #ddd', padding: 12, marginTop: 12 }}>
      <h3>{initial ? '编辑节点' : '新建节点'}</h3>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4, maxWidth: 480 }}>
        <label>名称 <input style={input} value={name} onChange={(e) => setName(e.target.value)} /></label>
        <label>类型
          <select style={input} value={kind} onChange={(e) => setKind(e.target.value as IntentKind)}>
            <option value="GROUP">分组</option>
            <option value="SYSTEM">内置</option>
            <option value="KB">知识库</option>
            <option value="MCP">MCP工具</option>
          </select>
        </label>
        {kind !== 'GROUP' && <label>code <input style={input} value={code} onChange={(e) => setCode(e.target.value)} /></label>}
        {kind === 'KB' && <label>知识库ID <input style={input} value={bind} onChange={(e) => setBind(e.target.value)} /></label>}
        {kind === 'MCP' && <label>mcpToolId <input style={input} value={bind} onChange={(e) => setBind(e.target.value)} /></label>}
        {kind !== 'GROUP' && <label>关键词(逗号分隔) <input style={input} value={kw} onChange={(e) => setKw(e.target.value)} /></label>}
        {kind !== 'GROUP' && <label>描述 <input style={input} value={desc} onChange={(e) => setDesc(e.target.value)} /></label>}
        {kind !== 'GROUP' && <label>示例(逗号分隔) <input style={input} value={ex} onChange={(e) => setEx(e.target.value)} /></label>}
        <label>排序 <input style={input} value={sort} onChange={(e) => setSort(e.target.value)} /></label>
        <label>父节点
          <select style={input} value={parent} onChange={(e) => setParent(e.target.value)}>
            <option value="">（顶层）</option>
            {groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
          </select>
        </label>
      </div>
      <div style={{ marginTop: 8 }}>
        <button onClick={submit}>保存</button>
        <button onClick={onCancel}>取消</button>
      </div>
    </div>
  )
}
