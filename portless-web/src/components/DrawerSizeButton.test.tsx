import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { DrawerSizeButton } from './DrawerSizeButton'

describe('DrawerSizeButton', () => {
  it('uses distinct icon-only controls for entering and leaving full screen', () => {
    const fullScreen = renderToStaticMarkup(createElement(DrawerSizeButton, { fullScreen: false, subject: 'checkout service', onToggle: () => undefined }))
    expect(fullScreen).toContain('aria-label="Full screen checkout service"')
    expect(fullScreen).toContain('aria-pressed="false"')
    expect(fullScreen).toContain('d="M6 2H2v4M10 2h4v4M2 10v4h4M14 10v4h-4"')
    expect(fullScreen).not.toContain('>FULL SCREEN<')

    const restore = renderToStaticMarkup(createElement(DrawerSizeButton, { fullScreen: true, subject: 'checkout service', onToggle: () => undefined }))
    expect(restore).toContain('aria-label="Restore checkout service"')
    expect(restore).toContain('aria-pressed="true"')
    expect(restore).toContain('d="M2 6h4V2M14 6h-4V2M2 10h4v4M14 10h-4v4"')
    expect(restore).not.toContain('>RESTORE<')
  })
})
