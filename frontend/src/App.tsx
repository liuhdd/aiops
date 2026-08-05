import { useEffect, useState } from 'react'
import AlertList from './components/AlertList'
import PipelineTimeline from './components/PipelineTimeline'
import { fetchAlerts, fetchAlert } from './api/client'
import { subscribeWS } from './api/ws'
import type { Alert, AlertDetail } from './types'

export default function App() {
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [selected, setSelected] = useState<string | null>(null)
  const [detail, setDetail] = useState<AlertDetail | null>(null)

  useEffect(() => {
    fetchAlerts().then(setAlerts).catch(() => {})
    const unsub = subscribeWS((m) => {
      if (m.type === 'alert') {
        fetchAlerts().then(setAlerts).catch(() => {})
      }
    })
    return unsub
  }, [])

  useEffect(() => {
    if (!selected) { setDetail(null); return }
    let cancelled = false
    const load = () => fetchAlert(selected).then((d) => { if (!cancelled) setDetail(d) }).catch(() => {})
    load()
    const unsub = subscribeWS((m) => {
      if (m.type === 'stage' || m.type === 'run') load()
    })
    return () => { cancelled = true; unsub() }
  }, [selected])

  return (
    <div style={{ fontFamily: 'system-ui, sans-serif' }}>
      <h1 style={{ padding: '8px 16px' }}>AIOps 硬件智能运维</h1>
      <div style={{ display: 'flex', gap: 16, padding: 16 }}>
        <div style={{ flex: 1 }}>
          <AlertList alerts={alerts} onSelect={setSelected} />
        </div>
        {selected
          ? (detail && <div style={{ flex: 1 }}><PipelineTimeline detail={detail} /></div>)
          : <div style={{ flex: 1 }}>请选择告警查看排查进度</div>}
      </div>
    </div>
  )
}
