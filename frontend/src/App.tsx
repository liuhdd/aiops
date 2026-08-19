import { useEffect, useState } from 'react'
import { Card, Empty, Layout, Menu, Typography } from 'antd'
import type { MenuProps } from 'antd'
import {
  AlertOutlined,
  DatabaseOutlined,
  NodeIndexOutlined,
} from '@ant-design/icons'
import AlertList from './components/AlertList'
import PipelineTimeline from './components/PipelineTimeline'
import IntentManager from './components/IntentManager'
import KnowledgeBase from './components/KnowledgeBase'
import StatsCards from './components/StatsCards'
import { fetchAlerts, fetchAlert } from './api/client'
import { subscribeWS } from './api/ws'
import type { Alert, AlertDetail } from './types'

const { Header, Sider, Content } = Layout

type View = 'ops' | 'intents' | 'kb'

const menuItems: MenuProps['items'] = [
  { key: 'ops', icon: <AlertOutlined />, label: '运维面板' },
  { key: 'intents', icon: <NodeIndexOutlined />, label: '意图管理' },
  { key: 'kb', icon: <DatabaseOutlined />, label: '知识库' },
]

export default function App() {
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [selected, setSelected] = useState<string | null>(null)
  const [detail, setDetail] = useState<AlertDetail | null>(null)
  const [view, setView] = useState<View>('ops')
  const [collapsed, setCollapsed] = useState(false)

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

  const ops =
    view === 'ops' ? (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        <StatsCards alerts={alerts} />
        <div style={{ display: 'flex', gap: 16 }}>
          <div style={{ flex: 2, minWidth: 0 }}>
            <AlertList alerts={alerts} onSelect={setSelected} selectedId={selected} />
          </div>
          <div style={{ flex: 3, minWidth: 0 }}>
            {selected
              ? (detail && <PipelineTimeline detail={detail} />)
              : (
                <Card>
                  <Empty description="请选择告警查看排查进度" />
                </Card>
              )}
          </div>
        </div>
      </div>
    ) : null

  const main =
    view === 'intents' ? <IntentManager />
    : view === 'kb' ? <KnowledgeBase />
    : ops

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Header style={{ display: 'flex', alignItems: 'center', gap: 12, paddingInline: 24 }}>
        <AlertOutlined style={{ fontSize: 22, color: '#1677ff' }} />
        <Typography.Title level={4} style={{ margin: 0, color: '#fff' }}>
          AIOps 硬件智能运维
        </Typography.Title>
      </Header>
      <Layout>
        <Sider collapsible collapsed={collapsed} onCollapse={setCollapsed} width={200}>
          <Menu
            theme="dark"
            mode="inline"
            selectedKeys={[view]}
            items={menuItems}
            onClick={({ key }) => setView(key as View)}
            style={{ borderInlineEnd: 'none' }}
          />
        </Sider>
        <Content style={{ padding: 16, overflow: 'auto' }}>{main}</Content>
      </Layout>
    </Layout>
  )
}
