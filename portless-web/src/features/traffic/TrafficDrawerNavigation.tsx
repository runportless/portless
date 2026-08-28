import { useEffect } from 'react'

function isEditableTarget(target: EventTarget | null) {
  if (!(target instanceof HTMLElement)) return false
  return target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName) || Boolean(target.closest('[contenteditable="true"]'))
}

export function useTrafficDrawerNavigationKeys<T>({ previous, next, pending, enabled = true, onNavigate }: {
  previous?: T
  next?: T
  pending: boolean
  enabled?: boolean
  onNavigate?: (item: T) => void
}) {
  useEffect(() => {
    if (!enabled || !onNavigate) return
    const keydown = (event: KeyboardEvent) => {
      if (pending || event.defaultPrevented || event.isComposing || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey || isEditableTarget(event.target)) return
      const destination = event.key === 'ArrowLeft' ? previous : event.key === 'ArrowRight' ? next : undefined
      if (!destination) return
      event.preventDefault()
      onNavigate(destination)
    }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [enabled, next, onNavigate, pending, previous])
}

export function TrafficNavigationArrow({ direction, boundary = false }: { direction: 'previous' | 'next'; boundary?: boolean }) {
  const previous = direction === 'previous'
  return <svg viewBox="0 0 16 16" aria-hidden="true">
    {boundary && <path d={previous ? 'M3 3v10' : 'M13 3v10'} />}
    <path d={previous ? 'm10.5 3.5-4.5 4.5 4.5 4.5' : 'm5.5 3.5 4.5 4.5-4.5 4.5'} />
  </svg>
}
