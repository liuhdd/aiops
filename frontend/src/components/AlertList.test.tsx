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
