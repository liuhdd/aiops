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
