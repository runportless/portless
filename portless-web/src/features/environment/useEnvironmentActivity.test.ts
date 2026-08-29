import { describe, expect, it } from 'vitest'
import type { Recording } from '../../api/contracts/experiments'
import { advanceRecordingCount } from './useEnvironmentActivity'

const active: Recording = {
  project: 'store',
  environment: 'local',
  name: 'checkout-debug',
  capturePayloads: false,
  maxEvents: 10,
  maxPayloadBytes: 0,
  status: 'active',
  startedAt: '2026-08-28T12:00:00Z',
  eventCount: 3,
}

describe('live recording activity', () => {
  it('increments only the active recording named by a captured exchange', () => {
    const completed = { ...active, name: 'older-debug', status: 'completed', eventCount: 8 }

    expect(advanceRecordingCount([completed, active], { recording: 'checkout-debug' })).toEqual([
      completed,
      { ...active, eventCount: 4 },
    ])
  })

  it('ignores ordinary traffic and never advances beyond the recording limit', () => {
    const full = { ...active, eventCount: active.maxEvents }

    expect(advanceRecordingCount([active], {})).toEqual([active])
    expect(advanceRecordingCount([full], { recording: active.name })).toEqual([full])
  })
})
