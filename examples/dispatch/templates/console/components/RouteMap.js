'use client'

export default function RouteMap({ pickup, destination }) {
  if (!pickup || !destination) return <div className="route-map route-map--empty">Choose two locations to preview a route.</div>
  const point = (location) => ({ x: 28 + location.x * 28, y: 18 + location.y * 20 })
  const from = point(pickup)
  const to = point(destination)
  return <svg className="route-map" viewBox="0 0 336 240" role="img" aria-label={`Route from ${pickup.name} to ${destination.name}`}>
    <defs><pattern id="grid" width="28" height="20" patternUnits="userSpaceOnUse"><path d="M 28 0 L 0 0 0 20" fill="none" stroke="currentColor" strokeWidth="0.5" /></pattern></defs>
    <rect width="336" height="240" fill="url(#grid)" />
    <path className="route-map__path" d={`M ${from.x} ${from.y} C ${from.x + 45} ${from.y}, ${to.x - 45} ${to.y}, ${to.x} ${to.y}`} />
    <circle className="route-map__origin" cx={from.x} cy={from.y} r="7" />
    <circle className="route-map__destination" cx={to.x} cy={to.y} r="7" />
    <text x={Math.min(from.x + 10, 245)} y={from.y - 10}>{pickup.name}</text>
    <text x={Math.min(to.x + 10, 245)} y={to.y + 18}>{destination.name}</text>
  </svg>
}

