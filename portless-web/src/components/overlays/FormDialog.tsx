import { useRef, type ReactNode, type RefObject } from 'react'
import { useOverlayDismiss } from './useOverlayDismiss'

export function FormDialog({ className = '', role = 'dialog', label, titleID, descriptionID, closeLabel, closeBlocked = false, initialFocusRef, restoreFocusRef, header, children, onClose }: {
  className?: string
  role?: 'dialog' | 'alertdialog'
  label?: string
  titleID?: string
  descriptionID?: string
  closeLabel: string
  closeBlocked?: boolean
  initialFocusRef?: RefObject<HTMLElement | null>
  restoreFocusRef?: RefObject<HTMLElement | null>
  header: ReactNode
  children: ReactNode
  onClose: () => void
}) {
  const surfaceRef = useRef<HTMLElement>(null)
  const { onBackdropMouseDown } = useOverlayDismiss({
    containerRef: surfaceRef,
    initialFocusRef,
    restoreFocusRef,
    dismissBlocked: closeBlocked,
    onDismiss: onClose,
  })
  const surfaceClass = ['form-modal', className].filter(Boolean).join(' ')

  return <div className="modal-backdrop form-modal-backdrop" role="presentation" onMouseDown={onBackdropMouseDown}>
    <section ref={surfaceRef} className={surfaceClass} role={role} aria-modal="true" aria-label={label} aria-labelledby={titleID} aria-describedby={descriptionID} tabIndex={-1} onMouseDown={(event) => event.stopPropagation()}>
      <header>{header}<button className="icon-button" type="button" aria-label={closeLabel} disabled={closeBlocked} onClick={onClose}>×</button></header>
      {children}
    </section>
  </div>
}
