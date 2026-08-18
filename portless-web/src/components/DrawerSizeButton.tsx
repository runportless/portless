export function DrawerSizeButton({ fullScreen, subject, onToggle }: { fullScreen: boolean; subject: string; onToggle: () => void }) {
  const action = fullScreen ? 'Restore' : 'Full screen'
  const label = `${action} ${subject}`
  return <button className="drawer-size-button" type="button" title={label} aria-label={label} aria-pressed={fullScreen} onClick={onToggle}><DrawerSizeIcon restore={fullScreen} /></button>
}

function DrawerSizeIcon({ restore }: { restore: boolean }) {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path d={restore ? 'M2 6h4V2M14 6h-4V2M2 10h4v4M14 10h-4v4' : 'M6 2H2v4M10 2h4v4M2 10v4h4M14 10v4h-4'} /></svg>
}
