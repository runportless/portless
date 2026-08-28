import { useEffect, useRef, type MouseEvent as ReactMouseEvent, type RefObject } from 'react'
import { focusOverlay, lockOverlayScroll, restoreOverlayFocus, trapOverlayFocus } from './overlayFocus'

type OverlayRegistration = { id: symbol; document: Document }

const overlayStack: OverlayRegistration[] = []

function isTopOverlay(id: symbol, document: Document) {
  for (let index = overlayStack.length - 1; index >= 0; index -= 1) {
    if (overlayStack[index].document === document) return overlayStack[index].id === id
  }
  return false
}

export function useOverlayDismiss({ containerRef, initialFocusRef, restoreFocusRef, dismissBlocked, onDismiss, onEscape = onDismiss }: {
  containerRef: RefObject<HTMLElement | null>
  initialFocusRef?: RefObject<HTMLElement | null>
  restoreFocusRef?: RefObject<HTMLElement | null>
  dismissBlocked: boolean
  onDismiss: () => void
  onEscape?: () => void
}) {
  const overlayID = useRef(Symbol('overlay'))
  const initialFocus = useRef<HTMLElement | null>(null)
  const dismissBlockedRef = useRef(dismissBlocked)
  const onDismissRef = useRef(onDismiss)
  const onEscapeRef = useRef(onEscape)
  dismissBlockedRef.current = dismissBlocked
  onDismissRef.current = onDismiss
  onEscapeRef.current = onEscape

  useEffect(() => {
    const container = containerRef.current
    if (!container) return
    const document = container.ownerDocument
    const view = document.defaultView
    if (!view) return
    const id = overlayID.current
    initialFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    overlayStack.push({ id, document })
    const unlockScroll = lockOverlayScroll(document)
    const focusFrame = view.requestAnimationFrame(() => focusOverlay(container, initialFocusRef?.current))

    const keydown = (event: KeyboardEvent) => {
      if (!isTopOverlay(id, document)) return
      if (event.key === 'Escape') {
        event.preventDefault()
        event.stopPropagation()
        if (!dismissBlockedRef.current) onEscapeRef.current()
        return
      }
      trapOverlayFocus(event, container)
    }
    const focusin = (event: FocusEvent) => {
      if (!isTopOverlay(id, document) || container.contains(event.target as Node)) return
      focusOverlay(container, initialFocusRef?.current)
    }
    document.addEventListener('keydown', keydown, true)
    document.addEventListener('focusin', focusin, true)

    return () => {
      view.cancelAnimationFrame(focusFrame)
      document.removeEventListener('keydown', keydown, true)
      document.removeEventListener('focusin', focusin, true)
      const index = overlayStack.findIndex((registration) => registration.id === id)
      if (index >= 0) overlayStack.splice(index, 1)
      unlockScroll()
      restoreOverlayFocus(restoreFocusRef?.current || initialFocus.current, view)
    }
  }, [containerRef, initialFocusRef, restoreFocusRef])

  const onBackdropMouseDown = (event: ReactMouseEvent<HTMLElement>) => {
    if (event.target !== event.currentTarget || dismissBlockedRef.current) return
    const container = containerRef.current
    if (!container || !isTopOverlay(overlayID.current, container.ownerDocument)) return
    onDismissRef.current()
  }

  return { onBackdropMouseDown }
}
