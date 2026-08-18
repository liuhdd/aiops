import { Card, Col, Row, Statistic } from 'antd'
import {
  AlertOutlined,
  CheckCircleOutlined,
  ExclamationCircleOutlined,
  InfoCircleOutlined,
} from '@ant-design/icons'
import type { Alert } from '../types'

export interface AlertStats {
  total: number
  critical: number
  warning: number
  info: number
}

export function deriveStats(alerts: Alert[]): AlertStats {
  return {
    total: alerts.length,
    critical: alerts.filter((a) => a.severity === 'critical').length,
    warning: alerts.filter((a) => a.severity === 'warning').length,
    info: alerts.filter((a) => a.severity === 'info').length,
  }
}

const cards = (s: AlertStats) => [
  { title: '告警总数', value: s.total, icon: <AlertOutlined />, color: '#1677ff' },
  { title: '严重', value: s.critical, icon: <ExclamationCircleOutlined />, color: '#ff4d4f' },
  { title: '警告', value: s.warning, icon: <CheckCircleOutlined />, color: '#fa8c16' },
  { title: '提示', value: s.info, icon: <InfoCircleOutlined />, color: '#1677ff' },
]

export default function StatsCards({ alerts }: { alerts: Alert[] }) {
  const s = deriveStats(alerts)
  return (
    <Row gutter={16}>
      {cards(s).map((c) => (
        <Col key={c.title} xs={12} md={6}>
          <Card size="small">
            <Statistic
              title={c.title}
              value={c.value}
              prefix={<span style={{ color: c.color }}>{c.icon}</span>}
            />
          </Card>
        </Col>
      ))}
    </Row>
  )
}
