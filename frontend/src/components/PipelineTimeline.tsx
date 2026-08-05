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

function StageRow({ s }: { s: StageResult }) {
  const rca = s.stage_id === 'rca' ? parseRCA(s.output) : null
  const ev = s.stage_id === 'evidence' ? parseEvidence(s.output) : null
  return (
    <div style={{ border: '1px solid #ddd', borderRadius: 8, padding: 8, marginBottom: 8 }}>
      <div>
        <strong>{s.stage_name}</strong>
        {s.iteration > 0 && <span style={{ color: '#f57c00' }}> 第{s.iteration}轮</span>}
        <span style={{ color: s.status === 'completed' ? '#2e7d32' : '#f57c00' }}>
          {' '}[{statusLabel[s.status] ?? s.status}]
        </span>
      </div>
      {s.error && <div style={{ color: '#d32f2f' }}>{s.error}</div>}
      {rca?.root_cause && (
        <div>
          <div>根因：{rca.root_cause}（置信度 {Math.round((rca.confidence ?? 0) * 100)}%）</div>
          <ul>{rca.evidence_chain?.map((e, i) => <li key={i}>{e}</li>)}</ul>
          {rca.gaps && rca.gaps.length > 0 && (
            <div style={{ color: '#f57c00' }}>待补充：{rca.gaps.join('、')}</div>
          )}
        </div>
      )}
      {ev && (
        <div style={{ color: '#555' }}>
          指标点 {ev.metrics?.length ?? 0} 个，日志 {ev.logs?.length ?? 0} 条
        </div>
      )}
    </div>
  )
}

export default function PipelineTimeline({ detail }: { detail: AlertDetail }) {
  return (
    <div>
      <div style={{ marginBottom: 8 }}>
        告警 {detail.alert.id} · 运行状态：{detail.run.status ?? '未运行'}
      </div>
      {detail.stages.length === 0 && <div style={{ color: '#888' }}>尚未开始排查</div>}
      {detail.stages.map((s) => <StageRow key={s.id} s={s} />)}
    </div>
  )
}
