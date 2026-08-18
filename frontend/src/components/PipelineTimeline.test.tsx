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
    expect(screen.getAllByText('完成').length).toBeGreaterThan(0)
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
