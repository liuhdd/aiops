import type { Alert } from '../types'

export function deriveStats(alerts: Alert[]) {
  const total = alerts.length
  const critical = alerts.filter((a) => a.severity === 'critical').length
  const warning = alerts.filter((a) => a.severity === 'warning').length
  const info = alerts.filter((a) => a.severity === 'info').length
  return { total, critical, warning, info }
}

export default function StatsCards({ alerts }: { alerts: Alert[] }) {
  void deriveStats(alerts)
  return null
}
