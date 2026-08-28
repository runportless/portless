const focusableSelector = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[contenteditable="true"]',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

const formControlSelector = [
  '[data-overlay-initial-focus]',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[contenteditable="true"]',
].join(',')

export function overlayFocusableElements(container: HTMLElement) {
  return Array.from(container.querySelectorAll<HTMLElement>(focusableSelector)).filter((element) => {
    return element.getAttribute('aria-hidden') !== 'true' && element.getClientRects().length > 0
  })
}

export function focusOverlay(container: HTMLElement, preferred?: HTMLElement | null) {
  if (preferred?.isConnected && container.contains(preferred) && !preferred.hasAttribute('disabled')) {
    preferred.focus()
    return
  }
  const formControl = container.querySelector<HTMLElement>(formControlSelector)
  const target = formControl || overlayFocusableElements(container)[0] || container
  target.focus()
}

export function trapOverlayFocus(event: KeyboardEvent, container: HTMLElement) {
  if (event.key !== 'Tab') return false
  const focusable = overlayFocusableElements(container)
  if (focusable.length === 0) {
    event.preventDefault()
    container.focus()
    return true
  }
  const currentIndex = focusable.indexOf(container.ownerDocument.activeElement as HTMLElement)
  const nextIndex = nextOverlayFocusIndex(focusable.length, currentIndex, event.shiftKey)
  if (nextIndex === null) return false
  event.preventDefault()
  focusable[nextIndex].focus()
  return true
}

export function nextOverlayFocusIndex(length: number, currentIndex: number, backwards: boolean) {
  if (length <= 0) return null
  if (backwards && currentIndex <= 0) return length - 1
  if (!backwards && (currentIndex < 0 || currentIndex >= length - 1)) return 0
  return null
}

const scrollLocks = new WeakMap<Document, { count: number; overflow: string }>()

export function lockOverlayScroll(document: Document) {
  const current = scrollLocks.get(document)
  if (current) current.count += 1
  else {
    scrollLocks.set(document, { count: 1, overflow: document.body.style.overflow })
    document.body.style.overflow = 'hidden'
  }
  return () => {
    const lock = scrollLocks.get(document)
    if (!lock) return
    lock.count -= 1
    if (lock.count > 0) return
    document.body.style.overflow = lock.overflow
    scrollLocks.delete(document)
  }
}

export function restoreOverlayFocus(element: HTMLElement | null, view: Window) {
  if (!element?.isConnected) return
  view.requestAnimationFrame(() => {
    if (element.isConnected) element.focus()
  })
}
