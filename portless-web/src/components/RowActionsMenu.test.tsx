import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { RowActionsMenu } from './RowActionsMenu'

describe('RowActionsMenu', () => {
  it('exposes a named menu trigger without rendering hidden actions', () => {
    const html = renderToStaticMarkup(<RowActionsMenu
      label="Recording actions for checkout-debug"
      menuLabel="checkout-debug recording actions"
      open={false}
      onOpenChange={() => undefined}
    ><button type="button" role="menuitem">DELETE</button></RowActionsMenu>)

    expect(html).toContain('aria-label="Recording actions for checkout-debug"')
    expect(html).toContain('aria-haspopup="menu"')
    expect(html).toContain('aria-expanded="false"')
    expect(html).toContain('class="more-actions-icon"')
    expect(html).not.toContain('role="menu"')
    expect(html).not.toContain('>DELETE</button>')
  })

  it('renders named menu items while open', () => {
    const html = renderToStaticMarkup(<RowActionsMenu
      label="Fault actions for slow-orders"
      menuLabel="slow-orders fault actions"
      open
      onOpenChange={() => undefined}
    >
      <a href="/export" role="menuitem" aria-label="Export slow-orders">EXPORT</a>
      <button className="is-danger" type="button" role="menuitem" aria-label="Delete slow-orders">DELETE</button>
    </RowActionsMenu>)

    expect(html).toContain('aria-expanded="true"')
    expect(html).toContain('class="row-actions-menu__menu" role="menu" aria-label="slow-orders fault actions"')
    expect(html).toContain('href="/export" role="menuitem" aria-label="Export slow-orders"')
    expect(html).toContain('role="menuitem" aria-label="Delete slow-orders"')
    expect(html).toContain('>DELETE</button>')
  })
})
