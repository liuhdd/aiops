# 前端页面美化（AntD 深色 O&M 风格）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 Ant Design v5 将全站（运维面板/意图管理/知识库）重构为深色 O&M 风格（顶栏 + 侧边栏布局），全部组件化。

**Architecture:** `main.tsx` 用 `ConfigProvider`（darkAlgorithm + 自定义 token）建全局主题；`App.tsx` 重构为 `Layout`（Header + 可折叠 Sider + Content）；各页面组件改用 antd 组件（Table/Card/Timeline/Tree/Modal/Form/Upload/Drawer），移除全部 inline style 中的视觉样式（功能性的如 `flex` 布局保留或迁移到类名）。不改后端、不改 `types.ts`、不改 `api/client.ts`。

**Tech Stack:** React 18、antd v6（计划写作时以 v5 语义编写，Task 1 已实测 v6 兼容全部所用 API）、@ant-design/icons、Vite 6、Vitest + jsdom + @testing-library/react。

## Global Constraints

- 所有命令在 `frontend/` 目录执行（`npm run dev` / `npx vitest run` / `npm run build`）。
- 不新增后端接口、不改 `src/types.ts`、不改 `src/api/client.ts`。
- 现有组件 props 契约尽量保持；`AlertList` 增加 `selectedId` 可选 prop（行高亮用）。
- 提交信息中文 + `feat:`/`test:`/`refactor:`/`docs:` 前缀。
- 测试必须在 jsdom 下通过；antd v5/v6 需要 `window.matchMedia`/`ResizeObserver` stub（Task 1 已建好，后续任务直接用）。
- 每个任务结束必须 `npx vitest run` 通过（或说明失败项），最后 `npm run build` 通过。
- 视觉样式统一走 `src/index.css` 的类名 + antd token；不再出现散落的 inline 视觉样式（`color`/`background`/`border` 等）。功能性 inline 样式（`display:flex`、`flex:2`、`width:360`）可保留。

---

### Task 1: 依赖、全局主题与测试基础设施 + 应用框架布局

**Files:**
- Create: `frontend/src/index.css`
- Create: `frontend/src/setupTests.ts`
- Modify: `frontend/package.json`（经 `npm install`）
- Modify: `frontend/vite.config.ts`
- Modify: `frontend/index.html`
- Modify: `frontend/src/main.tsx`
- Modify: `frontend/src/App.tsx`

**Interfaces:**
- Produces: 全局 `ConfigProvider` 主题（`darkAlgorithm`、`colorPrimary #1677ff`、`colorBgLayout #0f1420`、`colorBgContainer #1c2333`、`Layout.headerBg/siderBg #141a26`）；`setupTests.ts` 提供 `window.matchMedia`/`ResizeObserver` stub（所有后续测试依赖）；`App.tsx` 导出默认组件，内部 view state 仍为 `'ops' | 'intents' | 'kb'`。
- 无 Consumes。

- [ ] **Step 1: 安装依赖**

```bash
npm install antd @ant-design/icons
```

预期：`package.json` 新增 `antd` 与 `@ant-design/icons`。

- [ ] **Step 2: 创建测试基础设施 `src/setupTests.ts`**

```ts
import { afterEach } from 'vitest'
import { cleanup } from '@testing-library/react'

afterEach(() => cleanup())

if (typeof window !== 'undefined') {
  if (!window.matchMedia) {
    window.matchMedia = ((query: string): MediaQueryList => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    })) as unknown as (query: string) => MediaQueryList
  }
  if (!('ResizeObserver' in window)) {
    window.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver
  }
}
```

- [ ] **Step 3: vitest 挂 setupFiles**

修改 `frontend/vite.config.ts` 的 `test` 块：

```ts
test: {
  environment: 'jsdom',
  globals: true,
  setupFiles: ['./src/setupTests.ts'],
},
```

- [ ] **Step 4: 创建全局样式 `src/index.css`**

```css
:root {
  color-scheme: dark;
}

html,
body,
#root {
  height: 100%;
}

body {
  margin: 0;
  background: #0f1420;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC',
    'Hiragino Sans GB', 'Microsoft YaHei', sans-serif;
  -webkit-font-smoothing: antialiased;
}

*::-webkit-scrollbar {
  width: 8px;
  height: 8px;
}
*::-webkit-scrollbar-thumb {
  background: #3a4a63;
  border-radius: 4px;
}
*::-webkit-scrollbar-track {
  background: transparent;
}

.alert-row-selected > td {
  background: rgba(22, 119, 255, 0.16) !important;
}

.stage-row {
  padding: 8px 12px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 8px;
  background: #1c2333;
}

.chat-bubble {
  max-width: 80%;
  padding: 6px 10px;
  border-radius: 8px;
  background: #2a3346;
  color: #fff;
  white-space: pre-wrap;
}
.chat-bubble-user {
  background: #1677ff;
}
```

- [ ] **Step 5: `index.html` 加 color-scheme**

修改 `frontend/index.html`，在 `<meta name="viewport" ... />` 后加一行：

```html
<meta name="color-scheme" content="dark" />
```

- [ ] **Step 6: `main.tsx` 包裹 ConfigProvider**

替换 `frontend/src/main.tsx` 全部内容：

```tsx
import React from 'react'
import ReactDOM from 'react-dom/client'
import { ConfigProvider, theme } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import App from './App'
import './index.css'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ConfigProvider
      locale={zhCN}
      theme={{
        algorithm: theme.darkAlgorithm,
        token: {
          colorPrimary: '#1677ff',
          borderRadius: 6,
          colorBgLayout: '#0f1420',
          colorBgContainer: '#1c2333',
        },
        components: {
          Layout: { headerBg: '#141a26', siderBg: '#141a26' },
          Menu: { darkItemBg: '#141a26', darkItemSelectedBg: '#1677ff' },
        },
      }}
    >
      <App />
    </ConfigProvider>
  </React.StrictMode>,
)
```

- [ ] **Step 7: `App.tsx` 重构为 Header + Sider + Content 布局**

替换 `frontend/src/App.tsx` 全部内容：

```tsx
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
import ChatPanel from './components/ChatPanel'
import IntentManager from './components/IntentManager'
import KnowledgeBase from './components/KnowledgeBase'
import StatsCards from './components/StatsCards'
import { fetchAlerts, fetchAlert, fetchMessages, postChat } from './api/client'
import { subscribeWS } from './api/ws'
import type { Alert, AlertDetail, ChatMessage } from './types'

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
  const [messages, setMessages] = useState<ChatMessage[]>([])
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

  useEffect(() => {
    fetchMessages().then(setMessages).catch(() => {})
    const unsub = subscribeWS((m) => { if (m.type === 'message') fetchMessages().then(setMessages).catch(() => {}) })
    return unsub
  }, [])

  const handleSend = async (text: string) => {
    const reply = await postChat(text)
    const next = await fetchMessages()
    setMessages(next)
    return reply
  }

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
          <div style={{ width: 360 }}>
            <ChatPanel messages={messages} onSend={handleSend} />
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
```

- [ ] **Step 8: 创建 `StatsCards.tsx` 占位以编译通过（Task 2 完整实现）**

创建 `frontend/src/components/StatsCards.tsx`：

```tsx
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
```

- [ ] **Step 9: 运行现有测试与构建**

```bash
npx vitest run
npm run build
```

预期：`npx vitest run` 全部通过（现有 6 个测试未改，行为未变）；`npm run build` 成功（tsc + vite）。

- [ ] **Step 10: Commit**

```bash
git add frontend && git commit -m "feat: 引入antd深色主题与应用框架布局(顶栏+侧边栏)"
```

---

### Task 2: 统计卡片组件

**Files:**
- Modify: `frontend/src/components/StatsCards.tsx`
- Create: `frontend/src/components/StatsCards.test.tsx`

**Interfaces:**
- Produces: `deriveStats(alerts: Alert[]): { total: number; critical: number; warning: number; info: number }`；`StatsCards({ alerts }: { alerts: Alert[] })` 默认导出，渲染 4 张 `Card` + `Statistic`。
- Consumes: `Alert` 类型（`src/types.ts`）。

- [ ] **Step 1: 写失败测试**

创建 `frontend/src/components/StatsCards.test.tsx`：

```tsx
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import StatsCards, { deriveStats } from './StatsCards'
import type { Alert } from '../types'

const alert = (severity: string): Alert => ({
  id: `a-${severity}-${Math.random()}`,
  device: 'd',
  metric: 'm',
  severity,
  message: 'x',
  status: 'active',
  fired_at: '',
  created_at: '',
})

describe('deriveStats', () => {
  it('按 severity 计数', () => {
    const alerts = [alert('critical'), alert('critical'), alert('warning'), alert('info')]
    expect(deriveStats(alerts)).toEqual({ total: 4, critical: 2, warning: 1, info: 1 })
  })

  it('空列表全为 0', () => {
    expect(deriveStats([])).toEqual({ total: 0, critical: 0, warning: 0, info: 0 })
  })
})

describe('StatsCards', () => {
  it('渲染 4 张统计卡片', () => {
    const alerts = [alert('critical'), alert('warning')]
    render(<StatsCards alerts={alerts} />)
    expect(screen.getByText('告警总数')).toBeTruthy()
    expect(screen.getByText('2')).toBeTruthy()
    expect(screen.getByText('严重')).toBeTruthy()
    expect(screen.getByText('警告')).toBeTruthy()
    expect(screen.getByText('提示')).toBeTruthy()
  })
})
```

- [ ] **Step 2: 运行确认失败**

```bash
npx vitest run src/components/StatsCards.test.tsx
```

预期：FAIL（当前 `StatsCards` 返回 null，无文本）。

- [ ] **Step 3: 完整实现 `StatsCards.tsx`**

替换 `frontend/src/components/StatsCards.tsx`：

```tsx
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
```

- [ ] **Step 4: 运行确认通过**

```bash
npx vitest run src/components/StatsCards.test.tsx
```

预期：PASS。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/StatsCards.tsx frontend/src/components/StatsCards.test.tsx
git commit -m "feat: 运维面板统计卡片组件"
```

---

### Task 3: 告警列表改 AntD Table

**Files:**
- Modify: `frontend/src/components/AlertList.tsx`
- Modify: `frontend/src/components/AlertList.test.tsx`

**Interfaces:**
- Consumes: `Alert` 类型；`onSelect(id: string)`；新增可选 `selectedId?: string | null`。
- Produces: `AlertList({ alerts, onSelect, selectedId })` 默认导出，渲染 `Card` + `Table`，行点击调 `onSelect(a.id)`，`selectedId` 对应行加类名 `alert-row-selected`。

- [ ] **Step 1: 写失败测试**

替换 `frontend/src/components/AlertList.test.tsx`：

```tsx
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import AlertList from './AlertList'
import type { Alert } from '../types'

const alerts: Alert[] = [
  {
    id: 'alarm-1',
    device: 'compute-01',
    metric: 'cpu_usage',
    severity: 'warning',
    message: 'cpu high',
    status: 'active',
    fired_at: '2026-08-05T10:00:00Z',
    created_at: '2026-08-05T10:00:00Z',
  },
  {
    id: 'alarm-2',
    device: 'compute-02',
    metric: 'mem_usage',
    severity: 'critical',
    message: 'mem high',
    status: 'active',
    fired_at: '2026-08-05T10:00:00Z',
    created_at: '2026-08-05T10:00:00Z',
  },
]

describe('AlertList', () => {
  it('渲染告警并显示级别', () => {
    render(<AlertList alerts={alerts} onSelect={() => {}} />)
    expect(screen.getByText('alarm-1')).toBeTruthy()
    expect(screen.getByText('compute-01')).toBeTruthy()
    expect(screen.getByText('严重')).toBeTruthy()
  })

  it('空列表显示 Empty', () => {
    render(<AlertList alerts={[]} onSelect={() => {}} />)
    expect(screen.getByText('暂无告警')).toBeTruthy()
  })
})
```

- [ ] **Step 2: 运行确认失败**

```bash
npx vitest run src/components/AlertList.test.tsx
```

预期：FAIL（原实现无「严重」「暂无告警」文本、无 Card）。

- [ ] **Step 3: 重写 `AlertList.tsx`**

替换 `frontend/src/components/AlertList.tsx`：

```tsx
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
```

- [ ] **Step 4: 运行确认通过**

```bash
npx vitest run src/components/AlertList.test.tsx
```

预期：PASS。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/AlertList.tsx frontend/src/components/AlertList.test.tsx
git commit -m "feat: 告警列表改AntD表格并支持选中高亮"
```

---

### Task 4: 排查时间线改 AntD Card + Timeline + Descriptions

**Files:**
- Modify: `frontend/src/components/PipelineTimeline.tsx`
- Modify: `frontend/src/components/PipelineTimeline.test.tsx`

**Interfaces:**
- Consumes: `AlertDetail` 类型（`src/types.ts`）。
- Produces: `PipelineTimeline({ detail }: { detail: AlertDetail })` 默认导出；内部 `parseRCA(out: string): RCAOutput`、`parseEvidence(out: string): EvidenceOutput`（与现签名一致，供测试直接用）。

- [ ] **Step 1: 写失败测试**

替换 `frontend/src/components/PipelineTimeline.test.tsx`：

```tsx
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import PipelineTimeline from './PipelineTimeline'
import type { AlertDetail } from '../types'

const detail: AlertDetail = {
  alert: { id: 'alarm-1', device: 'd', metric: 'disk_usage', severity: 'critical',
    message: 'x', status: 'active', fired_at: '', created_at: '' },
  run: { id: 'run-alarm-1', alert_id: 'alarm-1', status: 'completed', iteration: 0,
    max_iteration: 2, created_at: '', updated_at: '' },
  stages: [
    { id: 's1', run_id: 'run-alarm-1', stage_id: 'rca', stage_name: '根因分析',
      status: 'completed', iteration: 0, output: '{"root_cause":"磁盘空间不足","evidence_chain":["e1"],"confidence":0.8,"gaps":[]}',
      started_at: '', ended_at: '' },
  ],
}

describe('PipelineTimeline', () => {
  it('渲染 rca 根因与运行状态', () => {
    render(<PipelineTimeline detail={detail} />)
    expect(screen.getByText(/磁盘空间不足/)).toBeTruthy()
    expect(screen.getByText('根因分析')).toBeTruthy()
    expect(screen.getByText('完成')).toBeTruthy()
  })

  it('无阶段时显示空态', () => {
    render(<PipelineTimeline detail={{ ...detail, stages: [] }} />)
    expect(screen.getByText('尚未开始排查')).toBeTruthy()
  })

  it('gaps 提示待补充', () => {
    const d: AlertDetail = {
      ...detail,
      stages: [{
        ...detail.stages[0],
        output: '{"root_cause":"磁盘空间不足","gaps":["证据不足"],"confidence":0.5}',
      }],
    }
    render(<PipelineTimeline detail={d} />)
    expect(screen.getByText(/待补充：证据不足/)).toBeTruthy()
  })
})
```

- [ ] **Step 2: 运行确认失败**

```bash
npx vitest run src/components/PipelineTimeline.test.tsx
```

预期：FAIL——`getByText('完成')` 断言失败（旧实现渲染的是原生 `run.status` 值 `completed`，无「完成」文案），即测试能区分新旧实现。

- [ ] **Step 3: 重写 `PipelineTimeline.tsx`**

替换 `frontend/src/components/PipelineTimeline.tsx`：

```tsx
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
        <Alert type="warning" showIcon style={{ marginTop: 8 }} message={`待补充：${rca.gaps.join('、')}`} />
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
            children: <StageRow s={s} />,
          }))}
        />
      )}
    </Card>
  )
}
```

- [ ] **Step 4: 运行确认通过**

```bash
npx vitest run src/components/PipelineTimeline.test.tsx
```

预期：PASS（三个用例全绿）。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/PipelineTimeline.tsx frontend/src/components/PipelineTimeline.test.tsx
git commit -m "feat: 排查时间线改AntD时间线与描述组件"
```

---

### Task 5: 聊天面板改 AntD Card + 气泡

**Files:**
- Modify: `frontend/src/components/ChatPanel.tsx`
- Modify: `frontend/src/components/ChatPanel.test.tsx`

**Interfaces:**
- Consumes: `ChatMessage` 类型；`onSend(text: string): Promise<ChatMessage>`。
- Produces: `ChatPanel({ messages, onSend })` 默认导出，渲染 `Card` + 气泡列表 + `Input.TextArea`（placeholder 精确为 `输入消息...`，Enter 发送）+ `Button`（文案 `发送`，发送中 loading）。

- [ ] **Step 1: 写失败测试**

替换 `frontend/src/components/ChatPanel.test.tsx`：

```tsx
import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import ChatPanel from './ChatPanel'
import type { ChatMessage } from '../types'

const msgs: ChatMessage[] = [
  { id: 'm1', role: 'user', content: '有哪些告警', created_at: '' },
  { id: 'm2', role: 'assistant', content: '当前没有告警。', created_at: '' },
]

describe('ChatPanel', () => {
  it('渲染消息并发送', async () => {
    const onSend = vi.fn().mockResolvedValue({
      id: 'm3', role: 'assistant', content: 'ok', created_at: '',
    } as ChatMessage)
    render(<ChatPanel messages={msgs} onSend={onSend} />)
    expect(screen.getByText('智能助手')).toBeTruthy()
    expect(screen.getByText('有哪些告警')).toBeTruthy()
    fireEvent.change(screen.getByPlaceholderText('输入消息...'), { target: { value: '你好' } })
    fireEvent.click(screen.getByText('发送'))
    await waitFor(() => expect(onSend).toHaveBeenCalledWith('你好'))
  })

  it('空消息显示引导语', () => {
    render(<ChatPanel messages={[]} onSend={vi.fn()} />)
    expect(screen.getByText(/开始对话/)).toBeTruthy()
  })
})
```

- [ ] **Step 2: 运行确认失败**

```bash
npx vitest run src/components/ChatPanel.test.tsx
```

预期：FAIL——`getByText('智能助手')` 断言失败（旧实现无 Card 标题文案），即测试能区分新旧实现。

- [ ] **Step 3: 重写 `ChatPanel.tsx`**

替换 `frontend/src/components/ChatPanel.tsx`：

```tsx
import { useState } from 'react'
import { Button, Card, Input, Space, Typography } from 'antd'
import { SendOutlined } from '@ant-design/icons'
import type { ChatMessage } from '../types'

export default function ChatPanel({
  messages,
  onSend,
}: {
  messages: ChatMessage[]
  onSend: (text: string) => Promise<ChatMessage>
}) {
  const [text, setText] = useState('')
  const [sending, setSending] = useState(false)

  const submit = async () => {
    if (!text.trim() || sending) return
    setSending(true)
    try {
      await onSend(text.trim())
      setText('')
    } finally {
      setSending(false)
    }
  }

  return (
    <Card title="智能助手" size="small" style={{ height: '100%' }}>
      <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
        <div style={{ flex: 1, overflowY: 'auto', minHeight: 200 }}>
          {messages.length === 0 && (
            <Typography.Text type="secondary">开始对话，例如：查一下 alarm-100 什么原因</Typography.Text>
          )}
          {messages.map((m) => (
            <div
              key={m.id}
              style={{
                display: 'flex',
                justifyContent: m.role === 'user' ? 'flex-end' : 'flex-start',
                margin: '6px 0',
              }}
            >
              <div className={`chat-bubble${m.role === 'user' ? ' chat-bubble-user' : ''}`}>
                {m.content}
              </div>
            </div>
          ))}
        </div>
        <Space.Compact style={{ width: '100%', marginTop: 8 }}>
          <Input.TextArea
            value={text}
            onChange={(e) => setText(e.target.value)}
            onPressEnter={(e) => {
              if (!e.shiftKey) {
                e.preventDefault()
                submit()
              }
            }}
            placeholder="输入消息..."
            autoSize={{ minRows: 1, maxRows: 4 }}
            aria-label="输入消息"
          />
          <Button type="primary" icon={<SendOutlined />} onClick={submit} loading={sending}>
            发送
          </Button>
        </Space.Compact>
      </div>
    </Card>
  )
}
```

- [ ] **Step 4: 运行确认通过**

```bash
npx vitest run src/components/ChatPanel.test.tsx
```

预期：PASS。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/ChatPanel.tsx frontend/src/components/ChatPanel.test.tsx
git commit -m "feat: 聊天面板改AntD卡片与气泡布局"
```

---

### Task 6: 意图树改 AntD Tree

**Files:**
- Modify: `frontend/src/components/IntentTree.tsx`
- Modify: `frontend/src/components/IntentTree.test.tsx`

**Interfaces:**
- Consumes: `IntentTreeNode` 类型；props `nodes/onToggle/onDelete/onAddChild/onEdit/error`（签名不变）。
- Produces: 保留导出 `collectIds(node): string[]`；`IntentTree` 用 antd `Tree` 渲染，节点 title 内联操作按钮（启停按钮文案 `启用`/`停用`，状态 `Tag` 文案 `已启用`/`已停用`，删除用 `Popconfirm` okText=`确定`）。

- [ ] **Step 1: 写失败测试**

替换 `frontend/src/components/IntentTree.test.tsx`：

```tsx
import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import IntentTree, { collectIds } from './IntentTree'
import type { IntentTreeNode } from '../types'

const leaf = (id: string, name: string): IntentTreeNode => ({
  id, parent_id: 'g', name, kind: 'KB', code: name, bind_target: '', rule_keywords: [],
  description: '', examples: [], enabled: true, deleted: false, sort_order: 0, created_at: '', updated_at: '', children: [],
})

const group: IntentTreeNode = {
  id: 'g', parent_id: '', name: '分组', kind: 'GROUP', code: '', bind_target: '', rule_keywords: [],
  description: '', examples: [], enabled: true, deleted: false, sort_order: 0, created_at: '', updated_at: '',
  children: [leaf('a', '意图A'), leaf('b', '意图B')],
}

describe('IntentTree', () => {
  it('collectIds 收集整棵子树', () => {
    expect(collectIds(group).sort()).toEqual(['a', 'b', 'g'].sort())
  })

  it('切换分组提交整棵子树 id', () => {
    const onToggle = vi.fn()
    render(<IntentTree nodes={[group]} onToggle={onToggle} onDelete={vi.fn()} onAddChild={vi.fn()} onEdit={vi.fn()} />)
    fireEvent.click(screen.getAllByText('停用')[0])
    expect(onToggle).toHaveBeenCalledWith(group, ['g', 'a', 'b'])
  })

  it('删除需经 Popconfirm 确认', () => {
    const onDelete = vi.fn()
    render(<IntentTree nodes={[group]} onToggle={vi.fn()} onDelete={onDelete} onAddChild={vi.fn()} onEdit={vi.fn()} />)
    fireEvent.click(screen.getAllByText('删除')[0])
    fireEvent.click(screen.getAllByText('确定')[0])
    expect(onDelete).toHaveBeenCalledWith(group, ['g', 'a', 'b'])
  })

  it('显示错误信息', () => {
    render(<IntentTree nodes={[group]} onToggle={vi.fn()} onDelete={vi.fn()} onAddChild={vi.fn()} onEdit={vi.fn()}
      error="子树不完整：节点 分组(g) 的后代 [a] 未包含" />)
    expect(screen.getByText(/子树不完整/)).toBeTruthy()
  })
})
```

- [ ] **Step 2: 运行确认失败**

```bash
npx vitest run src/components/IntentTree.test.tsx
```

预期：FAIL（原实现无 `删除` 按钮文案（是 `删除`？原实现按钮就是 `删除`，Popconfirm 场景新增；`已启用` 文案新增）。

- [ ] **Step 3: 重写 `IntentTree.tsx`**

替换 `frontend/src/components/IntentTree.tsx`：

```tsx
import { Alert, Button, Popconfirm, Space, Tag, Tree } from 'antd'
import type { TreeDataNode } from 'antd'
import type { IntentTreeNode } from '../types'

export function collectIds(node: IntentTreeNode): string[] {
  const ids = [node.id]
  for (const c of node.children) ids.push(...collectIds(c))
  return ids
}

const kindBadge: Record<string, string> = {
  GROUP: '分组', SYSTEM: '内置', KB: '知识库', MCP: 'MCP',
}
const kindColor: Record<string, string> = {
  GROUP: 'geekblue', SYSTEM: 'purple', KB: 'cyan', MCP: 'orange',
}

export default function IntentTree({
  nodes, onToggle, onDelete, onAddChild, onEdit, error,
}: {
  nodes: IntentTreeNode[]
  onToggle: (node: IntentTreeNode, ids: string[]) => void
  onDelete: (node: IntentTreeNode, ids: string[]) => void
  onAddChild: (node: IntentTreeNode) => void
  onEdit: (node: IntentTreeNode) => void
  error?: string
}) {
  const toData = (list: IntentTreeNode[]): TreeDataNode[] =>
    list.map((n) => {
      const isGroup = n.kind === 'GROUP'
      return {
        key: n.id,
        title: (
          <Space size={8} wrap>
            <span style={{ fontWeight: isGroup ? 600 : 400 }}>{n.name}</span>
            <Tag color={kindColor[n.kind]} style={{ marginInlineEnd: 0 }}>{kindBadge[n.kind]}</Tag>
            {!isGroup && <span style={{ color: 'rgba(255,255,255,0.45)' }}>{n.code}</span>}
            <Tag color={n.enabled ? 'success' : 'default'} style={{ marginInlineEnd: 0 }}>
              {n.enabled ? '已启用' : '已停用'}
            </Tag>
            <Button size="small" onClick={() => onToggle(n, collectIds(n))}>
              {n.enabled ? '停用' : '启用'}
            </Button>
            {isGroup && <Button size="small" onClick={() => onAddChild(n)}>+子节点</Button>}
            <Button size="small" onClick={() => onEdit(n)}>编辑</Button>
            <Popconfirm
              title="确认删除？"
              okText="确定"
              onConfirm={() => onDelete(n, collectIds(n))}
            >
              <Button size="small" danger>删除</Button>
            </Popconfirm>
          </Space>
        ),
        children: n.children.length ? toData(n.children) : undefined,
      }
    })

  return (
    <div>
      {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 8 }} />}
      {nodes.length === 0 ? (
        <div style={{ color: 'rgba(255,255,255,0.45)', padding: 16 }}>暂无意图节点</div>
      ) : (
        <Tree treeData={toData(nodes)} defaultExpandAll showLine blockNode />
      )}
    </div>
  )
}
```

- [ ] **Step 4: 运行确认通过**

```bash
npx vitest run src/components/IntentTree.test.tsx
```

预期：PASS。若「删除需经 Popconfirm」失败（antd Popconfirm 弹层时序），改为在 `onConfirm` 前先 `await screen.findByText('确认删除？')` 再点击 `确定`，确保弹层已挂载。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/IntentTree.tsx frontend/src/components/IntentTree.test.tsx
git commit -m "feat: 意图树改AntD树组件并加删除确认"
```

---

### Task 7: 意图编辑器改 AntD Modal + Form，接线 IntentManager

**Files:**
- Modify: `frontend/src/components/IntentEditor.tsx`
- Modify: `frontend/src/components/IntentEditor.test.tsx`
- Modify: `frontend/src/components/IntentManager.tsx`

**Interfaces:**
- Consumes: `IntentTreeNode`、`IntentKind`、`IntentUpsert` 类型。
- Produces:
  - `IntentEditor({ open, initial?, groups, onSubmit, onCancel })`：受控 Modal；props 含必填 `open: boolean`；导出 `parseExamples(str: string): string[]` 与 `parseKeywords(str: string): string[]` 纯函数。
  - `IntentManager` 始终渲染 `<IntentEditor open={editing !== null} initial={editing?.node} parentId={editing?.parentId} ... />`，`onCancel={() => setEditing(null)}`。

- [ ] **Step 1: 写失败测试**

替换 `frontend/src/components/IntentEditor.test.tsx`：

```tsx
import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import IntentEditor, { parseExamples, parseKeywords } from './IntentEditor'

describe('parseExamples / parseKeywords', () => {
  it('按中英文逗号/换行拆分并去空', () => {
    expect(parseExamples('怎么查手册, 手册在哪\n 第三个，第四')).toEqual(['怎么查手册', '手册在哪', '第三个', '第四'])
    expect(parseKeywords('温控,风扇, 电源')).toEqual(['温控', '风扇', '电源'])
  })
})

describe('IntentEditor', () => {
  it('切换类型为知识库后填写示例并提交', async () => {
    const onSubmit = vi.fn()
    render(<IntentEditor open groups={[]} onSubmit={onSubmit} onCancel={vi.fn()} />)
    fireEvent.mouseDown(screen.getByLabelText(/类型/))
    fireEvent.click(await screen.findByText('知识库'))
    fireEvent.change(screen.getByLabelText(/示例/), { target: { value: '怎么查手册, 手册在哪' } })
    fireEvent.click(screen.getByText('保存'))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ examples: ['怎么查手册', '手册在哪'], kind: 'KB' }),
    ))
  })

  it('名称必填校验阻止提交', async () => {
    const onSubmit = vi.fn()
    render(<IntentEditor open groups={[]} onSubmit={onSubmit} onCancel={vi.fn()} />)
    fireEvent.click(screen.getByText('保存'))
    expect(onSubmit).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: 运行确认失败**

```bash
npx vitest run src/components/IntentEditor.test.tsx
```

预期：FAIL（`parseExamples` 未导出；原组件无 Modal/Form）。

- [ ] **Step 3: 重写 `IntentEditor.tsx`**

替换 `frontend/src/components/IntentEditor.tsx`：

```tsx
import { useState } from 'react'
import { Button, Form, Input, Modal, Select } from 'antd'
import type { IntentTreeNode, IntentKind, IntentUpsert } from '../types'

export function parseExamples(str: string): string[] {
  return str.split(/[,，\n]/).map((s) => s.trim()).filter(Boolean)
}
export function parseKeywords(str: string): string[] {
  return str.split(/[,，]/).map((s) => s.trim()).filter(Boolean)
}

export default function IntentEditor({
  open, initial, groups, onSubmit, onCancel,
}: {
  open: boolean
  initial?: IntentTreeNode
  groups: IntentTreeNode[]
  onSubmit: (v: IntentUpsert) => void
  onCancel: () => void
}) {
  const [form] = Form.useForm<IntentUpsert>()
  const [kind, setKind] = useState<IntentKind>(initial?.kind ?? 'GROUP')

  const submit = async () => {
    const values = await form.validateFields()
    onSubmit({
      ...values,
      rule_keywords: Array.isArray(values.rule_keywords)
        ? values.rule_keywords
        : parseKeywords(values.rule_keywords ?? ''),
      examples: Array.isArray(values.examples)
        ? values.examples
        : parseExamples(values.examples ?? ''),
    })
  }

  return (
    <Modal
      open={open}
      title={initial ? '编辑节点' : '新建节点'}
      onOk={submit}
      onCancel={onCancel}
      okText="保存"
      cancelText="取消"
      destroyOnHidden
    >
      <Form
        form={form}
        layout="vertical"
        initialValues={{
          parent_id: initial?.parent_id ?? '',
          name: initial?.name ?? '',
          kind: initial?.kind ?? 'GROUP',
          code: initial?.code ?? '',
          bind_target: initial?.bind_target ?? '',
          rule_keywords: (initial?.rule_keywords ?? []).join(', '),
          description: initial?.description ?? '',
          examples: (initial?.examples ?? []).join(', '),
          sort_order: initial?.sort_order ?? 0,
        }}
      >
        <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]}>
          <Input />
        </Form.Item>
        <Form.Item name="kind" label="类型">
          <Select
            options={[
              { value: 'GROUP', label: '分组' },
              { value: 'SYSTEM', label: '内置' },
              { value: 'KB', label: '知识库' },
              { value: 'MCP', label: 'MCP工具' },
            ]}
            onChange={(v: IntentKind) => setKind(v)}
          />
        </Form.Item>
        {kind !== 'GROUP' && (
          <>
            <Form.Item name="code" label="code">
              <Input />
            </Form.Item>
            {kind === 'KB' && (
              <Form.Item name="bind_target" label="知识库ID">
                <Input />
              </Form.Item>
            )}
            {kind === 'MCP' && (
              <Form.Item name="bind_target" label="mcpToolId">
                <Input />
              </Form.Item>
            )}
            <Form.Item name="rule_keywords" label="关键词(逗号分隔)">
              <Input />
            </Form.Item>
            <Form.Item name="description" label="描述">
              <Input />
            </Form.Item>
            <Form.Item name="examples" label="示例(逗号分隔)">
              <Input />
            </Form.Item>
          </>
        )}
        <Form.Item name="sort_order" label="排序">
          <Input type="number" />
        </Form.Item>
        <Form.Item name="parent_id" label="父节点">
          <Select
            options={[
              { value: '', label: '（顶层）' },
              ...groups.map((g) => ({ value: g.id, label: g.name })),
            ]}
          />
        </Form.Item>
      </Form>
    </Modal>
  )
}
```

- [ ] **Step 4: 接线 `IntentManager.tsx`**

替换 `frontend/src/components/IntentManager.tsx` 中 render 部分（整文件替换）：

```tsx
import { useCallback, useEffect, useState } from 'react'
import { Alert, Space, Typography } from 'antd'
import IntentTree from './IntentTree'
import IntentEditor from './IntentEditor'
import { fetchIntentTree, createIntent, updateIntent, deleteIntents, setIntentStatus } from '../api/client'
import type { IntentTreeNode, IntentUpsert } from '../types'

function collectGroups(nodes: IntentTreeNode[]): IntentTreeNode[] {
  const out: IntentTreeNode[] = []
  for (const n of nodes) {
    if (n.kind === 'GROUP') {
      out.push(n)
      out.push(...collectGroups(n.children))
    }
  }
  return out
}

export default function IntentManager() {
  const [nodes, setNodes] = useState<IntentTreeNode[]>([])
  const [error, setError] = useState('')
  const [editing, setEditing] = useState<{ node?: IntentTreeNode; parentId?: string } | null>(null)

  const reload = useCallback(async () => {
    try {
      setNodes(await fetchIntentTree())
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
  }, [])

  useEffect(() => { reload() }, [reload])

  const toggle = async (node: IntentTreeNode, ids: string[]) => {
    try {
      await setIntentStatus(ids, !node.enabled)
      await reload()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const remove = async (node: IntentTreeNode, ids: string[]) => {
    try {
      await deleteIntents(ids)
      await reload()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const submit = async (v: IntentUpsert) => {
    try {
      const body: IntentUpsert = {
        ...v,
        rule_keywords: Array.isArray(v.rule_keywords) ? v.rule_keywords : [],
        examples: Array.isArray(v.examples) ? v.examples : [],
      }
      if (editing?.node) await updateIntent(editing.node.id, body)
      else await createIntent({ ...body, parent_id: editing?.parentId ?? body.parent_id })
      setEditing(null)
      await reload()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <div>
      <Typography.Title level={4}>意图管理</Typography.Title>
      {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 8 }} />}
      <IntentTree
        nodes={nodes}
        onToggle={toggle}
        onDelete={remove}
        onAddChild={(n) => setEditing({ parentId: n.id })}
        onEdit={(n) => setEditing({ node: n })}
      />
      <IntentEditor
        open={editing !== null}
        initial={editing?.node}
        groups={collectGroups(nodes)}
        onSubmit={submit}
        onCancel={() => setEditing(null)}
      />
    </div>
  )
}
```

- [ ] **Step 5: 运行确认通过**

```bash
npx vitest run src/components/IntentEditor.test.tsx
npx vitest run src/components/IntentTree.test.tsx
```

预期：全部 PASS。若 rc-select 交互（`fireEvent.mouseDown` + `findByText('知识库')`）不稳定，改为先 `await waitFor(() => expect(screen.getByLabelText(/类型/)).toBeTruthy())` 再 mouseDown。

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/IntentEditor.tsx frontend/src/components/IntentEditor.test.tsx frontend/src/components/IntentManager.tsx
git commit -m "feat: 意图编辑器改AntD模态表单并接线管理页"
```

---

### Task 8: 知识库页面改 AntD Table + Upload + Drawer

**Files:**
- Modify: `frontend/src/components/KnowledgeBase.tsx`
- Modify: `frontend/src/components/KnowledgeBase.test.tsx`

**Interfaces:**
- Consumes: `KbDocSummary`、`KbDocDetail`、`KbDocUpsert`、`KbFilter`、`IntentLeaf` 类型；`fetchKbDocs/fetchKbDoc/uploadKbDoc/updateKbDoc/deleteKbDoc/fetchIntentLeaves`（`src/api/client.ts`）。
- Produces: `KnowledgeBase()` 默认导出：筛选 `Select`/`Input`、上传 `Card` + `Upload`（`beforeUpload={() => false}`）、列表 `Table`、详情 `Drawer`；内部导出 `CATEGORIES` 供测试引用（若需要）。

- [ ] **Step 1: 写失败测试**

替换 `frontend/src/components/KnowledgeBase.test.tsx`：

```tsx
import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import KnowledgeBase from './KnowledgeBase'
import { fetchKbDocs, uploadKbDoc, deleteKbDoc } from '../api/client'
import type { KbDocSummary } from '../types'

vi.mock('../api/client', () => ({
  fetchKbDocs: vi.fn(),
  fetchKbDoc: vi.fn(),
  uploadKbDoc: vi.fn(),
  updateKbDoc: vi.fn(),
  deleteKbDoc: vi.fn(),
  fetchIntentLeaves: vi.fn().mockResolvedValue([]),
}))

const doc = (id: string, title: string, category: KbDocSummary['category']): KbDocSummary => ({
  id, title, category, device_types: ['switch'], vendor: ['H3C'], model: ['S5560'],
  metrics: [], keywords: ['光模块'], intents: [], source_file: '', content: '',
  created_at: '', updated_at: '', chunk_count: 2,
})

describe('KnowledgeBase', () => {
  it('渲染文档列表', async () => {
    vi.mocked(fetchKbDocs).mockResolvedValue([doc('d1', '交换机手册', 'manual')])
    render(<KnowledgeBase />)
    expect(await screen.findByText('交换机手册')).toBeTruthy()
    expect(screen.getByText(/2 块/)).toBeTruthy()
  })

  it('筛选变更调用 fetchKbDocs', async () => {
    vi.mocked(fetchKbDocs).mockResolvedValue([])
    render(<KnowledgeBase />)
    const q = await screen.findByPlaceholderText('关键词')
    fireEvent.change(q, { target: { value: '光模块' } })
    await waitFor(() => {
      expect(fetchKbDocs).toHaveBeenCalledWith(expect.objectContaining({ q: '光模块' }))
    })
  })

  it('上传成功后刷新列表', async () => {
    vi.mocked(fetchKbDocs).mockResolvedValue([])
    vi.mocked(uploadKbDoc).mockResolvedValue({} as never)
    const { container } = render(<KnowledgeBase />)
    const file = new File(['x'], 'a.md', { type: 'text/markdown' })
    const input = container.querySelector('input[type="file"]') as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })
    fireEvent.click(screen.getByText('上传'))
    await waitFor(() => expect(uploadKbDoc).toHaveBeenCalled())
  })

  it('删除经确认后调用 deleteKbDoc 并刷新', async () => {
    vi.mocked(fetchKbDocs).mockResolvedValue([doc('d1', '交换机手册', 'manual')])
    vi.mocked(deleteKbDoc).mockResolvedValue({ status: 'ok' })
    render(<KnowledgeBase />)
    await screen.findByText('交换机手册')
    fireEvent.click(screen.getByText('删除'))
    fireEvent.click(await screen.findByText('确定'))
    await waitFor(() => expect(deleteKbDoc).toHaveBeenCalledWith('d1'))
  })

  it('点击详情打开 Drawer', async () => {
    vi.mocked(fetchKbDocs).mockResolvedValue([doc('d1', '交换机手册', 'manual')])
    vi.mocked(fetchKbDoc).mockResolvedValue({
      ...doc('d1', '交换机手册', 'manual'),
      chunks: [{ id: 'c1', doc_id: 'd1', seq: 1, heading: '概述', content: '本手册介绍交换机', created_at: '' }],
    })
    render(<KnowledgeBase />)
    await screen.findByText('交换机手册')
    fireEvent.click(screen.getByText('详情'))
    expect(await screen.findByText('本手册介绍交换机')).toBeTruthy()
  })
})
```

- [ ] **Step 2: 运行确认失败**

```bash
npx vitest run src/components/KnowledgeBase.test.tsx
```

预期：FAIL（原实现无 Drawer、Popconfirm 确认「确定」按钮、`input[type=file]` 结构差异）。

- [ ] **Step 3: 重写 `KnowledgeBase.tsx`**

替换 `frontend/src/components/KnowledgeBase.tsx`：

```tsx
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
      {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 8 }} />}

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
              <Button onClick={() => onDelete(detail.id)} danger>删除文档</Button>
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
            {detail.chunks.map((c) => (
              <div key={c.id} style={{ marginTop: 8, padding: 8, borderRadius: 6, background: '#2a3346' }}>
                {c.heading && <Typography.Text strong>{c.heading}</Typography.Text>}
                <div>{c.content}</div>
              </div>
            ))}
          </>
        )}
      </Drawer>
    </div>
  )
}
```

- [ ] **Step 4: 运行确认通过**

```bash
npx vitest run src/components/KnowledgeBase.test.tsx
```

预期：PASS（五个用例全绿）。若上传用例失败（`fireEvent.change` 未触发 antd Upload onChange），改用 `fireEvent.change(input, { target: { files: [file] } })` 后先 `await waitFor(() => expect(uploadKbDoc).toHaveBeenCalled())` 已有；若仍需触发内部 onChange，可改为对 `input` 分派 `change` 事件并 `await act(async () => {})`。若 Drawer 用例因动画未挂载，在断言前加 `await screen.findByText('本手册介绍交换机')`（已用）。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/KnowledgeBase.tsx frontend/src/components/KnowledgeBase.test.tsx
git commit -m "feat: 知识库页改AntD表格/上传/抽屉"
```

---

### Task 9: 全量验证

**Files:**
- 无新增/修改（仅验证）。

- [ ] **Step 1: 全量单测**

```bash
npx vitest run
```

预期：全部 PASS（9 个测试文件）。

- [ ] **Step 2: 类型与构建**

```bash
npm run build
```

预期：tsc + vite 构建成功，无类型错误。

- [ ] **Step 3: 人工走查**

启动 `npm run dev`（需后端 `:8080` 已运行，或至少页面能打开）：
- 运维面板：深色主题、4 张统计卡片、告警表选中高亮、时间线、聊天发送
- 意图管理：树展开/收起、启停、编辑 Modal、删除确认
- 知识库：筛选、上传、详情 Drawer、删除确认

- [ ] **Step 4: Commit（如有遗留样式微调）**

```bash
git add frontend
git commit -m "refactor: 全站暗色主题与组件化收尾微调"
```

---

## Self-Review 记录

- **Spec 覆盖**：主题/依赖（Task 1）、框架布局 Header+Sider（Task 1）、统计卡片（Task 2）、告警列表 Table（Task 3）、时间线 Card/Timeline/Descriptions（Task 4）、聊天面板（Task 5）、意图树 Tree + Popconfirm（Task 6）、编辑器 Modal+Form（Task 7）、知识库 Table/Upload/Drawer（Task 8）、测试适配与 matchMedia polyfill（Task 1）、验证命令（Task 9）。统计口径与 spec 修正后一致（纯 alerts 派生）。
- **占位符扫描**：无 TBD/TODO；所有代码步骤含完整实现。
- **类型一致性**：`deriveStats` 返回 `{ total, critical, warning, info }`（Task 2 定义，Task 1 占位同签名）；`collectIds` 在 Task 6 保持导出；`IntentEditor` 新 props `open` 在 Task 7 定义并被 Task 7 IntentManager 使用；`AlertList` 的 `selectedId` 在 Task 3 定义并被 Task 1 App 使用；antd 类型统一用 `TreeDataNode`（`antd` 导出）、`ColumnsType`（`antd/es/table`）。
