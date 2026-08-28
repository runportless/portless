import { useRef, useState, type HTMLAttributes, type ReactNode, type Ref } from 'react'
import { DrawerSizeButton } from '../DrawerSizeButton'
import { useOverlayDismiss } from './useOverlayDismiss'

export function DrawerShell({ label, subject, className = '', closeLabel = 'Close', closeBlocked = false, header, actions, actionProps, notice, tabs, contentClassName = '', contentProps, contentRef, children, onClose }: {
  label: string
  subject: string
  className?: string
  closeLabel?: string
  closeBlocked?: boolean
  header: ReactNode
  actions?: ReactNode
  actionProps?: Omit<HTMLAttributes<HTMLDivElement>, 'children' | 'className'>
  notice?: ReactNode
  tabs?: ReactNode
  contentClassName?: string
  contentProps?: Omit<HTMLAttributes<HTMLDivElement>, 'children' | 'className'>
  contentRef?: Ref<HTMLDivElement>
  children: ReactNode
  onClose: () => void
}) {
  const [fullScreen, setFullScreen] = useState(false)
  const surfaceRef = useRef<HTMLElement>(null)
  const { onBackdropMouseDown } = useOverlayDismiss({
    containerRef: surfaceRef,
    initialFocusRef: surfaceRef,
    dismissBlocked: closeBlocked,
    onDismiss: onClose,
    onEscape: () => fullScreen ? setFullScreen(false) : onClose(),
  })
  const surfaceClass = ['drawer', className, fullScreen ? 'drawer--fullscreen' : ''].filter(Boolean).join(' ')
  const bodyClass = ['drawer-content', contentClassName].filter(Boolean).join(' ')

  return <div className="drawer-backdrop" role="presentation" onMouseDown={onBackdropMouseDown}>
    <aside ref={surfaceRef} className={surfaceClass} role="dialog" aria-modal="true" aria-label={label} tabIndex={-1} onMouseDown={(event) => event.stopPropagation()}>
      <header>{header}<div className="drawer-header-actions"><DrawerSizeButton fullScreen={fullScreen} subject={subject} onToggle={() => setFullScreen((value) => !value)} /><button className="icon-button" type="button" disabled={closeBlocked} onClick={onClose} aria-label={closeLabel}>×</button></div></header>
      {actions && <div className="drawer-actions" {...actionProps}>{actions}</div>}
      {notice}
      {tabs}
      <div ref={contentRef} className={bodyClass} {...contentProps}>{children}</div>
    </aside>
  </div>
}
