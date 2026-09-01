import { useEffect, useRef, type MouseEvent as ReactMouseEvent, type ReactNode } from 'react'
import { MoreActionsIcon } from './MoreActionsIcon'

export function RowActionsMenu({ label, menuLabel, open, disabled = false, onOpenChange, children }: {
  label: string
  menuLabel: string
  open: boolean
  disabled?: boolean
  onOpenChange: (open: boolean) => void
  children: ReactNode
}) {
  const root = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (!open) return
    const dismissOutside = (event: MouseEvent) => {
      if (!root.current?.contains(event.target as Node)) onOpenChange(false)
    }
    const dismissWithEscape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      onOpenChange(false)
      window.requestAnimationFrame(() => trigger.current?.focus())
    }
    document.addEventListener('mousedown', dismissOutside)
    window.addEventListener('keydown', dismissWithEscape)
    return () => {
      document.removeEventListener('mousedown', dismissOutside)
      window.removeEventListener('keydown', dismissWithEscape)
    }
  }, [onOpenChange, open])

  const stopRowAction = (event: ReactMouseEvent<HTMLDivElement>) => event.stopPropagation()

  return <div ref={root} className="row-actions-menu" onClick={stopRowAction}>
    <button
      ref={trigger}
      className="row-actions-menu__trigger"
      type="button"
      aria-label={label}
      aria-haspopup="menu"
      aria-expanded={open}
      disabled={disabled}
      onClick={() => onOpenChange(!open)}
    ><MoreActionsIcon /></button>
    {open && <div className="row-actions-menu__menu" role="menu" aria-label={menuLabel}>{children}</div>}
  </div>
}
