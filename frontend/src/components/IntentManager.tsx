import { useCallback, useEffect, useState } from 'react'
import { Alert, Space, Typography } from 'antd'
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
      const body: IntentUpsert = {
        ...v,
        rule_keywords: Array.isArray(v.rule_keywords) ? v.rule_keywords : [],
        examples: Array.isArray(v.examples) ? v.examples : [],
      }
      if (editing?.node) await updateIntent(editing.node.id, body)
      else await createIntent({ ...body, parent_id: editing?.parentId ?? body.parent_id })
      setEditing(null)
      await reload()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div>
      <Typography.Title level={4}>意图管理</Typography.Title>
      {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 8 }} />}
      <IntentTree
        nodes={nodes}
        onToggle={toggle}
        onDelete={remove}
        onAddChild={(n) => setEditing({ parentId: n.id })}
        onEdit={(n) => setEditing({ node: n })}
      />
      <IntentEditor
        open={editing !== null}
        initial={editing?.node}
        groups={collectGroups(nodes)}
        onSubmit={submit}
        onCancel={() => setEditing(null)}
      />
    </div>
  )
}
