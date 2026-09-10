import { useCallback, useEffect, useState } from 'react'
import {
  Alert, Button, Card, Descriptions, Drawer, Empty, Input, Popconfirm, Select,
  Space, Table, Tag, Typography, Upload,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { UploadOutlined } from '@ant-design/icons'
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

const categoryColor: Record<string, string> = {
  case: 'cyan', manual: 'blue', runbook: 'geekblue', other: 'default',
}

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
  const [file, setFile] = useState<File | null>(null)
  const [meta, setMeta] = useState({ category: '', title: '', vendor: '', model: '', intent: '' })

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
    if (!file) { setError('请选择文件'); return }
    const form = new FormData()
    form.append('file', file)
    if (meta.category) form.append('category', meta.category)
    if (meta.title) form.append('title', meta.title)
    if (meta.vendor) form.append('vendor', meta.vendor)
    if (meta.model) form.append('model', meta.model)
    if (meta.intent) form.append('intents', meta.intent)
    setUploading(true)
    try {
      await uploadKbDoc(form)
      setFile(null)
      setMeta({ category: '', title: '', vendor: '', model: '', intent: '' })
      reload()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setUploading(false)
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

  const columns: ColumnsType<KbDocSummary> = [
    { title: '标题', dataIndex: 'title', key: 'title' },
    {
      title: '分类', dataIndex: 'category', key: 'category',
      render: (v: string) => <Tag color={categoryColor[v] ?? 'default'}>{CATEGORIES.find((c) => c.value === v)?.label ?? v}</Tag>,
    },
    { title: '设备类型', dataIndex: 'device_types', key: 'device_types', render: tags },
    { title: '厂商', dataIndex: 'vendor', key: 'vendor', render: tags },
    { title: '型号', dataIndex: 'model', key: 'model', render: tags },
    { title: '块数', dataIndex: 'chunk_count', key: 'chunk_count', render: (v: number) => `${v} 块` },
    { title: '来源', dataIndex: 'source_file', key: 'source_file' },
    {
      title: '操作', key: 'actions',
      render: (_: unknown, d: KbDocSummary) => (
        <Space>
          <Button size="small" onClick={() => onOpen(d.id)}>详情</Button>
          <Popconfirm title="确认删除？" okText="确定" onConfirm={() => onDelete(d.id)}>
            <Button size="small" danger>删除</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ]

  return (
    <div>
      <Typography.Title level={4}>知识库</Typography.Title>
      {error && <Alert type="error" showIcon title={error} style={{ marginBottom: 8 }} />}

      <Space wrap style={{ marginBottom: 16 }}>
        <Select
          aria-label="筛选分类"
          placeholder="全部分类"
          style={{ width: 160 }}
          allowClear
          options={CATEGORIES.filter((c) => c.value !== '').map((c) => ({ value: c.value, label: c.label }))}
          onChange={(v?: string) => setFilter({ ...filter, category: v || undefined })}
        />
        <Input placeholder="厂商" aria-label="厂商"
          onChange={(e) => setFilter({ ...filter, vendor: e.target.value || undefined })} style={{ width: 140 }} />
        <Input placeholder="型号" aria-label="型号"
          onChange={(e) => setFilter({ ...filter, model: e.target.value || undefined })} style={{ width: 140 }} />
        <Input placeholder="关键词" aria-label="关键词"
          onChange={(e) => setFilter({ ...filter, q: e.target.value || undefined })} style={{ width: 160 }} />
      </Space>

      <Card title="上传文档" size="small" style={{ marginBottom: 16 }}>
        <Space wrap>
          <Upload
            accept=".txt,.md,.docx"
            maxCount={1}
            beforeUpload={() => false}
            onChange={(info) => {
              const f = info.fileList[0]
              setFile(f ? (f.originFileObj as File) ?? (f as unknown as File) : null)
            }}
            onRemove={() => setFile(null)}
            fileList={file ? [{ uid: '-1', name: file.name, status: 'done' }] : []}
          >
            <Button icon={<UploadOutlined />}>选择文件</Button>
          </Upload>
          <Select aria-label="上传分类" placeholder="默认：其他" style={{ width: 140 }}
            value={meta.category || undefined} allowClear
            options={[
              { value: 'case', label: '运维案例' },
              { value: 'manual', label: '产品手册' },
              { value: 'runbook', label: '操作手册' },
            ]}
            onChange={(v?: string) => setMeta({ ...meta, category: v ?? '' })} />
          <Input placeholder="标题（留空用文件名）" aria-label="标题" value={meta.title}
            onChange={(e) => setMeta({ ...meta, title: e.target.value })} style={{ width: 180 }} />
          <Input placeholder="厂商（逗号分隔）" aria-label="上传厂商" value={meta.vendor}
            onChange={(e) => setMeta({ ...meta, vendor: e.target.value })} style={{ width: 160 }} />
          <Input placeholder="型号（逗号分隔）" aria-label="上传型号" value={meta.model}
            onChange={(e) => setMeta({ ...meta, model: e.target.value })} style={{ width: 160 }} />
          <Select aria-label="绑定意图" placeholder="不绑定意图" style={{ width: 140 }} allowClear
            value={meta.intent || undefined}
            options={intents.map((i) => ({ value: i.code, label: i.name || i.code }))}
            onChange={(v?: string) => setMeta({ ...meta, intent: v ?? '' })} />
          <Button type="primary" onClick={onUpload} disabled={uploading} loading={uploading}>上传</Button>
        </Space>
      </Card>

      <Table rowKey="id" columns={columns} dataSource={docs} size="small" pagination={false} />

      <Drawer
        open={detail !== null}
        onClose={() => setDetail(null)}
        width={640}
        title={detail?.title ?? ''}
      >
        {detail && (
          <>
            <Space wrap style={{ marginBottom: 8 }}>
              <Popconfirm title="确认删除？" okText="确定" onConfirm={() => onDelete(detail.id)}>
                <Button danger>删除文档</Button>
              </Popconfirm>
              <Button onClick={() => onSaveMeta(detail.id, {
                title: detail.title, category: detail.category,
                device_types: detail.device_types, vendor: detail.vendor, model: detail.model,
                metrics: detail.metrics, keywords: detail.keywords, intents: detail.intents,
              })}>保存元数据</Button>
            </Space>
            <Descriptions column={1} size="small">
              <Descriptions.Item label="标签">
                {tags(detail.device_types)} / {tags(detail.vendor)} / {tags(detail.model)} / {tags(detail.intents)}
              </Descriptions.Item>
            </Descriptions>
            {detail.chunks.length === 0 && <Empty description="暂无分块" />}
            {detail.chunks.map((c) => {
              const srcIds = c.source_doc_ids ?? []
              return (
                <div key={c.id} style={{ marginTop: 8, padding: 8, borderRadius: 6, background: '#2a3346' }}>
                  {c.heading && <Typography.Text strong>{c.heading}</Typography.Text>}
                  {c.summary && (
                    <div style={{ marginTop: 4 }}>
                      <Typography.Text type="secondary">摘要：{c.summary}</Typography.Text>
                    </div>
                  )}
                  <div style={{ marginTop: 4 }}>{c.content}</div>
                  {srcIds.length > 1 && (
                    <div style={{ marginTop: 4 }}>
                      <Typography.Text type="secondary">多文档合并（{srcIds.length} 个来源）：{srcIds.join(', ')}</Typography.Text>
                    </div>
                  )}
                  {srcIds.length === 1 && srcIds[0] !== detail.id && (
                    <div style={{ marginTop: 4 }}>
                      <Typography.Text type="secondary">来源：{srcIds[0]}</Typography.Text>
                    </div>
                  )}
                </div>
              )
            })}
          </>
        )}
      </Drawer>
    </div>
  )
}
