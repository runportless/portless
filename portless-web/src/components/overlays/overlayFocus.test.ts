import { describe, expect, it } from 'vitest'
import { nextOverlayFocusIndex } from './overlayFocus'

describe('overlay focus containment', () => {
  it('wraps forward and backward at the focus boundary', () => {
    expect(nextOverlayFocusIndex(3, 2, false)).toBe(0)
    expect(nextOverlayFocusIndex(3, 0, true)).toBe(2)
  })

  it('enters the focus sequence from the overlay surface', () => {
    expect(nextOverlayFocusIndex(3, -1, false)).toBe(0)
    expect(nextOverlayFocusIndex(3, -1, true)).toBe(2)
  })

  it('leaves focus alone away from a boundary', () => {
    expect(nextOverlayFocusIndex(3, 1, false)).toBeNull()
    expect(nextOverlayFocusIndex(3, 1, true)).toBeNull()
    expect(nextOverlayFocusIndex(0, -1, false)).toBeNull()
  })
})
