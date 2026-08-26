import { describe, expect, it } from 'vitest'
import { duration, statusTone } from './Status'

describe('status presentation', () => {
  it('maps domain states to consistent safety tones', () => {
    expect(statusTone('healthy')).toBe('success')
    expect(statusTone('starting')).toBe('warning')
    expect(statusTone('checking')).toBe('warning')
    expect(statusTone('failed')).toBe('danger')
  })

  it('formats traffic durations', () => {
    expect(duration(42)).toBe('42ms')
    expect(duration(1250)).toBe('1.25s')
  })
})
