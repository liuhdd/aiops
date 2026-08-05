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
]

describe('AlertList', () => {
  it('renders alerts', () => {
    render(<AlertList alerts={alerts} onSelect={() => {}} />)
    expect(screen.getByText('alarm-1')).toBeTruthy()
    expect(screen.getByText('compute-01')).toBeTruthy()
  })
})
