import { useCallback, useEffect, useState } from 'react'
import IntentTree from './IntentTree'
import IntentEditor from './IntentEditor'
import { fetchIntentTree, createIntent, updateIntent, deleteIntents, setIntentStatus } from '../api/client'
import type { IntentTreeNode, IntentUpsert } from '../types'

function collectGroups(nodes: IntentTreeNode[]): IntentTreeNode[] {
  const out: IntentTreeNode[] = []
  for (const n of nodes) {
    if (n.kind === 'GROUP') {
      out.push(n)
      out.push(...collectGroups(n.children))
    }
  }
  return out
}

export default function IntentManager() {
  const [nodes, setNodes] = useState<IntentTreeNode[]>([])
  const [error, setError] = useState('')
  const [editing, setEditing] = useState<{ node?: IntentTreeNode; parentId?: string } | null>(null)

  const reload = useCallback(async () => {
    try {
      setNodes(await fetchIntentTree())
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
  }, [])

  useEffect(() => { reload() }, [reload])

  const toggle = async (node: IntentTreeNode, ids: string[]) => {
    try {
      await setIntentStatus(ids, !node.enabled)
      await reload()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const remove = async (node: IntentTreeNode, ids: string[]) => {
    try {
      await deleteIntents(ids)
      await reload()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const submit = async (v: IntentUpsert) => {
    try {
      if (editing?.node) await updateIntent(editing.node.id, v)
      else await createIntent({ ...v, parent_id: editing?.parentId ?? v.parent_id })
      setEditing(null)
      await reload()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div style={{ padding: 16 }}>
      <h2>意图管理</h2>
      {error && <div style={{ color: '#d32f2f', padding: 8 }}>{error}</div>}
      <IntentTree
        nodes={nodes}
        onToggle={toggle}
        onDelete={remove}
        onAddChild={(n) => setEditing({ parentId: n.id })}
        onEdit={(n) => setEditing({ node: n })}
      />
      {editing && (
        <IntentEditor
          initial={editing.node}
          groups={collectGroups(nodes)}
          onSubmit={submit}
          onCancel={() => setEditing(null)}
        />
      )}
    </div>
  )
}
