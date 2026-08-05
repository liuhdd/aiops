import { useEffect, useState } from 'react'
import AlertList from './components/AlertList'
import { fetchAlerts } from './api/client'
import { subscribeWS } from './api/ws'
import type { Alert } from './types'

export default function App() {
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [selected, setSelected] = useState<string | null>(null)

  useEffect(() => {
    fetchAlerts().then(setAlerts).catch(() => {})
    const unsub = subscribeWS((m) => {
      if (m.type === 'alert') {
        fetchAlerts().then(setAlerts).catch(() => {})
      }
    })
    return unsub
  }, [])

  return (
    <div style={{ fontFamily: 'system-ui, sans-serif' }}>
      <h1 style={{ padding: '8px 16px' }}>AIOps 硬件智能运维</h1>
      <div style={{ display: 'flex', gap: 16, padding: 16 }}>
        <div style={{ flex: 1 }}>
          <AlertList alerts={alerts} onSelect={setSelected} />
        </div>
        {selected && <div style={{ flex: 1 }}>选中的告警：{selected}</div>}
      </div>
    </div>
  )
}
