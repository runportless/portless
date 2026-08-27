export function TrafficNavigationArrow({ direction, boundary = false }: { direction: 'previous' | 'next'; boundary?: boolean }) {
  const previous = direction === 'previous'
  return <svg viewBox="0 0 16 16" aria-hidden="true">
    {boundary && <path d={previous ? 'M3 3v10' : 'M13 3v10'} />}
    <path d={previous ? 'm10.5 3.5-4.5 4.5 4.5 4.5' : 'm5.5 3.5 4.5 4.5-4.5 4.5'} />
  </svg>
}
