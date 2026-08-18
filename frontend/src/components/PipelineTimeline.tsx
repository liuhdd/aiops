import { Alert, Card, Descriptions, Empty, Space, Tag, Timeline, Typography } from 'antd'
import type { AlertDetail, StageResult } from '../types'

interface RCAOutput {
  root_cause?: string
  evidence_chain?: string[]
  confidence?: number
  gaps?: string[]
}
interface EvidenceOutput {
  metrics?: unknown[]
  logs?: unknown[]
  gaps?: string[]
}

function parseRCA(out: string): RCAOutput {
  try { return JSON.parse(out) as RCAOutput } catch { return {} }
}
function parseEvidence(out: string): EvidenceOutput {
  try { return JSON.parse(out) as EvidenceOutput } catch { return {} }
}

const statusLabel: Record<string, string> = {
  running: '进行中', completed: '完成', failed: '失败', skipped: '跳过', pending: '待执行',
}
const statusColor: Record<string, string> = {
  running: 'processing', completed: 'success', failed: 'error', skipped: 'default', pending: 'default',
}
const runStatusMap: Record<string, { color: string; text: string }> = {
  running: { color: 'processing', text: '运行中' },
  completed: { color: 'success', text: '完成' },
  failed: { color: 'error', text: '失败' },
  skipped: { color: 'default', text: '跳过' },
  degraded: { color: 'warning', text: '降级' },
}

function StageRow({ s }: { s: StageResult }) {
  const rca = s.stage_id === 'rca' ? parseRCA(s.output) : null
  const ev = s.stage_id === 'evidence' ? parseEvidence(s.output) : null
  return (
    <div className="stage-row">
      <Space wrap>
        <Typography.Text strong>{s.stage_name}</Typography.Text>
        {s.iteration > 0 && <Tag color="orange">第{s.iteration}轮</Tag>}
        <Tag color={statusColor[s.status] ?? 'default'}>{statusLabel[s.status] ?? s.status}</Tag>
      </Space>
      {s.error && <div style={{ color: '#ff7875', marginTop: 4 }}>{s.error}</div>}
      {rca?.root_cause && (
        <Descriptions size="small" column={1} style={{ marginTop: 8 }}>
          <Descriptions.Item label="根因">{rca.root_cause}</Descriptions.Item>
          <Descriptions.Item label="置信度">
            {Math.round((rca.confidence ?? 0) * 100)}%
          </Descriptions.Item>
          <Descriptions.Item label="证据链">
            {rca.evidence_chain?.length ? (
              <ul style={{ margin: 0, paddingLeft: 16 }}>{rca.evidence_chain.map((e, i) => <li key={i}>{e}</li>)}</ul>
            ) : '—'}
          </Descriptions.Item>
        </Descriptions>
      )}
      {rca?.gaps && rca.gaps.length > 0 && (
        <Alert type="warning" showIcon style={{ marginTop: 8 }} title={`待补充：${rca.gaps.join('、')}`} />
      )}
      {ev && (
        <div style={{ marginTop: 4, color: 'rgba(255,255,255,0.65)' }}>
          指标点 {ev.metrics?.length ?? 0} 个，日志 {ev.logs?.length ?? 0} 条
        </div>
      )}
    </div>
  )
}

export default function PipelineTimeline({ detail }: { detail: AlertDetail }) {
  const { alert, run, stages } = detail
  const rs = runStatusMap[run.status] ?? { color: 'default', text: run.status ?? '未运行' }
  return (
    <Card
      title={(
        <Space>
          <span>告警 {alert.id}</span>
          <Tag color={rs.color}>{rs.text}</Tag>
        </Space>
      )}
      size="small"
    >
      {stages.length === 0 && <Empty description="尚未开始排查" />}
      {stages.length > 0 && (
        <Timeline
          items={stages.map((s) => ({
            color: s.status === 'completed' ? 'green' : s.status === 'failed' ? 'red' : 'blue',
            content: <StageRow s={s} />,
          }))}
        />
      )}
    </Card>
  )
}
