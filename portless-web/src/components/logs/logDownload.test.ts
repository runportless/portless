import { describe, expect, it, vi } from 'vitest'
import { logBlob, openRawLog, rawLogURLLifetimeMilliseconds } from './logDownload'

describe('raw log downloads', () => {
  it('creates exact plain-text blobs', async () => {
    const blob = logBlob('first\nsecond\n')

    expect(blob.type).toBe('text/plain;charset=utf-8')
    expect(await blob.text()).toBe('first\nsecond\n')
  })

  it('opens the blob in a new tab and revokes its URL after the retention window', async () => {
    let revoke: (() => void) | undefined
    let createdBlob: Blob | undefined
    const environment = {
      createObjectURL: vi.fn((blob: Blob) => { createdBlob = blob; return 'blob:logs' }),
      open: vi.fn(),
      revokeObjectURL: vi.fn(),
      setTimeout: vi.fn((callback: () => void) => { revoke = callback }),
    }

    openRawLog('exact log output\n', environment)

    expect(await createdBlob?.text()).toBe('exact log output\n')
    expect(environment.open).toHaveBeenCalledWith('blob:logs', '_blank', 'noopener,noreferrer')
    expect(environment.setTimeout).toHaveBeenCalledWith(expect.any(Function), rawLogURLLifetimeMilliseconds)
    expect(environment.revokeObjectURL).not.toHaveBeenCalled()
    revoke?.()
    expect(environment.revokeObjectURL).toHaveBeenCalledWith('blob:logs')
  })
})
