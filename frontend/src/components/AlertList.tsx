import { Card, Empty, Table, Tag, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import type { Alert } from '../types'

const severityTag: Record<string, { color: string; text: string }> = {
  critical: { color: 'red', text: '严重' },
  warning: { color: 'orange', text: '警告' },
  info: { color: 'blue', text: '提示' },
}

export default function AlertList({
  alerts,
  onSelect,
  selectedId,
}: {
  alerts: Alert[]
  onSelect: (id: string) => void
  selectedId?: string | null
}) {
  const columns: ColumnsType<Alert> = [
    { title: 'ID', dataIndex: 'id', key: 'id' },
    { title: '设备', dataIndex: 'device', key: 'device' },
    { title: '指标', dataIndex: 'metric', key: 'metric' },
    {
      title: '级别',
      dataIndex: 'severity',
      key: 'severity',
      render: (v: string) => {
        const t = severityTag[v]
        return t ? <Tag color={t.color}>{t.text}</Tag> : <Tag>{v}</Tag>
      },
    },
    { title: '消息', dataIndex: 'message', key: 'message' },
    {
      title: '时间',
      dataIndex: 'fired_at',
      key: 'fired_at',
      render: (v: string) => <Typography.Text type="secondary">{new Date(v).toLocaleString()}</Typography.Text>,
    },
  ]

  if (alerts.length === 0) {
    return (
      <Card title="告警列表" size="small">
        <Empty description="暂无告警" />
      </Card>
    )
  }

  return (
    <Card title="告警列表" size="small">
      <Table
        rowKey="id"
        columns={columns}
        dataSource={alerts}
        size="small"
        pagination={false}
        onRow={(a) => ({
          onClick: () => onSelect(a.id),
          style: { cursor: 'pointer' },
        })}
        rowClassName={(a) => (a.id === selectedId ? 'alert-row-selected' : '')}
      />
    </Card>
  )
}
