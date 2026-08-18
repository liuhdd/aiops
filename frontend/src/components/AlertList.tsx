import type { Alert } from '../types'

const severityColor: Record<string, string> = {
  critical: '#d32f2f',
  warning: '#f57c00',
  info: '#1976d2',
}

export default function AlertList({
  alerts,
  onSelect,
}: {
  alerts: Alert[]
  onSelect: (id: string) => void
  selectedId?: string | null
}) {
  if (alerts.length === 0) {
    return <div style={{ padding: 16, color: '#888' }}>暂无告警</div>
  }
  return (
    <table style={{ width: '100%', borderCollapse: 'collapse' }}>
      <thead>
        <tr>
          <th>ID</th>
          <th>设备</th>
          <th>指标</th>
          <th>级别</th>
          <th>消息</th>
          <th>时间</th>
        </tr>
      </thead>
      <tbody>
        {alerts.map((a) => (
          <tr key={a.id} onClick={() => onSelect(a.id)} style={{ cursor: 'pointer' }}>
            <td>{a.id}</td>
            <td>{a.device}</td>
            <td>{a.metric}</td>
            <td style={{ color: severityColor[a.severity] ?? '#333' }}>{a.severity}</td>
            <td>{a.message}</td>
            <td>{new Date(a.fired_at).toLocaleString()}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
