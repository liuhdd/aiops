import { useCallback, useEffect, useState } from 'react'
import {
  fetchKbDocs, fetchKbDoc, uploadKbDoc, updateKbDoc, deleteKbDoc, fetchIntentLeaves,
} from '../api/client'
import type { KbDocSummary, KbDocDetail, KbDocUpsert, KbFilter, IntentLeaf } from '../types'

const CATEGORIES: { value: string; label: string }[] = [
  { value: '', label: '全部分类' },
  { value: 'case', label: '运维案例' },
  { value: 'manual', label: '产品手册' },
  { value: 'runbook', label: '操作手册' },
  { value: 'other', label: '其他' },
]

function tags(list: string[] | undefined): string {
  return (list ?? []).join(', ')
}

export default function KnowledgeBase() {
  const [docs, setDocs] = useState<KbDocSummary[]>([])
  const [detail, setDetail] = useState<KbDocDetail | null>(null)
  const [intents, setIntents] = useState<IntentLeaf[]>([])
  const [error, setError] = useState('')
  const [filter, setFilter] = useState<KbFilter>({})
  const [uploading, setUploading] = useState(false)
  const [upload, setUpload] = useState({ file: null as File | null, category: '', title: '', vendor: '', model: '', intent: '' })

  const reload = useCallback(async () => {
    try {
      setDocs(await fetchKbDocs(filter))
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
  }, [filter])

  useEffect(() => { reload() }, [reload])

  useEffect(() => {
    fetchIntentLeaves().then(setIntents).catch(() => {})
  }, [])

  const onUpload = async () => {
    if (!upload.file) { setError('请选择文件'); return }
    const form = new FormData()
    form.append('file', upload.file)
    if (upload.category) form.append('category', upload.category)
    if (upload.title) form.append('title', upload.title)
    if (upload.vendor) form.append('vendor', upload.vendor)
    if (upload.model) form.append('model', upload.model)
    if (upload.intent) form.append('intents', upload.intent)
    setUploading(true)
    try {
      await uploadKbDoc(form)
      setUpload({ file: null, category: '', title: '', vendor: '', model: '', intent: '' })
      setUploading(false)
      reload()
    } catch (e) {
      setUploading(false)
      setError((e as Error).message)
    }
  }

  const onOpen = async (id: string) => {
    try {
      setDetail(await fetchKbDoc(id))
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const onDelete = async (id: string) => {
    try {
      await deleteKbDoc(id)
      setDetail(null)
      reload()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const onSaveMeta = async (id: string, body: KbDocUpsert) => {
    try {
      await updateKbDoc(id, body)
      setDetail(await fetchKbDoc(id))
      reload()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div style={{ padding: 16 }}>
      <h2>知识库</h2>
      {error && <div style={{ color: '#d32f2f', padding: 8 }}>{error}</div>}

      <div style={{ marginBottom: 16, display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <select aria-label="筛选分类" value={filter.category ?? ''}
          onChange={(e) => setFilter({ ...filter, category: e.target.value || undefined })}>
          {CATEGORIES.map((c) => <option key={c.value} value={c.value}>{c.label}</option>)}
        </select>
        <input placeholder="厂商" aria-label="厂商"
          onChange={(e) => setFilter({ ...filter, vendor: e.target.value || undefined })} />
        <input placeholder="型号" aria-label="型号"
          onChange={(e) => setFilter({ ...filter, model: e.target.value || undefined })} />
        <input placeholder="关键词" aria-label="关键词"
          onChange={(e) => setFilter({ ...filter, q: e.target.value || undefined })} />
      </div>

      <div style={{ marginBottom: 16, padding: 12, border: '1px solid #ddd', borderRadius: 8 }}>
        <div>上传文档（.txt / .md / .docx）</div>
        <input type="file" accept=".txt,.md,.docx" aria-label="上传文件"
          onChange={(e) => setUpload({ ...upload, file: e.target.files?.[0] ?? null })} />
        <select aria-label="上传分类" value={upload.category}
          onChange={(e) => setUpload({ ...upload, category: e.target.value })}>
          <option value="">默认：其他</option>
          <option value="case">运维案例</option>
          <option value="manual">产品手册</option>
          <option value="runbook">操作手册</option>
        </select>
        <input placeholder="标题（留空用文件名）" aria-label="标题"
          value={upload.title} onChange={(e) => setUpload({ ...upload, title: e.target.value })} />
        <input placeholder="厂商（逗号分隔）" aria-label="上传厂商"
          value={upload.vendor} onChange={(e) => setUpload({ ...upload, vendor: e.target.value })} />
        <input placeholder="型号（逗号分隔）" aria-label="上传型号"
          value={upload.model} onChange={(e) => setUpload({ ...upload, model: e.target.value })} />
        <select aria-label="绑定意图" value={upload.intent}
          onChange={(e) => setUpload({ ...upload, intent: e.target.value })}>
          <option value="">不绑定意图</option>
          {intents.map((i) => <option key={i.code} value={i.code}>{i.name || i.code}</option>)}
        </select>
        <button onClick={onUpload} disabled={uploading}>{uploading ? '上传中…' : '上传'}</button>
      </div>

      {detail && (
        <div style={{ marginBottom: 16, padding: 12, border: '1px solid #ccc', borderRadius: 8 }}>
          <h3>{detail.title}</h3>
          <button onClick={() => setDetail(null)}>关闭</button>
          <button onClick={() => onDelete(detail.id)}>删除文档</button>
          <button onClick={() => onSaveMeta(detail.id, {
            title: detail.title, category: detail.category,
            device_types: detail.device_types, vendor: detail.vendor, model: detail.model,
            metrics: detail.metrics, keywords: detail.keywords, intents: detail.intents,
          })}>保存元数据</button>
          <div>标签：{tags(detail.device_types)} / {tags(detail.vendor)} / {tags(detail.model)} / {tags(detail.intents)}</div>
          {detail.chunks.map((c) => (
            <div key={c.id} style={{ marginTop: 8, padding: 8, background: '#f6f6f6' }}>
              {c.heading && <strong>{c.heading}</strong>}
              <div>{c.content}</div>
            </div>
          ))}
        </div>
      )}

      <table style={{ width: '100%', borderCollapse: 'collapse' }}>
        <thead>
          <tr>
            <th>标题</th><th>分类</th><th>设备类型</th><th>厂商</th><th>型号</th><th>块数</th><th>来源</th><th></th>
          </tr>
        </thead>
        <tbody>
          {docs.map((d) => (
            <tr key={d.id} style={{ borderTop: '1px solid #eee' }}>
              <td>{d.title}</td>
              <td>{d.category}</td>
              <td>{tags(d.device_types)}</td>
              <td>{tags(d.vendor)}</td>
              <td>{tags(d.model)}</td>
              <td>{d.chunk_count} 块</td>
              <td>{d.source_file}</td>
              <td>
                <button onClick={() => onOpen(d.id)}>详情</button>
                <button onClick={() => onDelete(d.id)}>删除</button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}