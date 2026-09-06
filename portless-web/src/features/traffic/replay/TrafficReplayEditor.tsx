import { useEffect, useId, useRef, useState, type RefObject } from 'react'
import type { Environment } from '../../../api/contracts/environments'
import { ActionErrorNotice } from '../../../components/ActionError'
import { FormDialog } from '../../../components/overlays/FormDialog'
import { useOverlayDismiss } from '../../../components/overlays/useOverlayDismiss'
import { ReplayHeaderEditor } from './ReplayHeaderEditor'
import { ReplayTabs } from './ReplayTabs'
import { TrafficReplayResult } from './TrafficReplayResult'
import { capturedReplayBodyAvailable, replayDestinationReason, replayDraftFingerprint, replayMethods } from './trafficReplayDraft'
import type { useTrafficReplay } from './useTrafficReplay'

export function TrafficReplayEditor({ replay, environments, restoreFocusRef, onClose }: { replay: ReturnType<typeof useTrafficReplay>; environments: Environment[]; restoreFocusRef?: RefObject<HTMLElement | null>; onClose: () => void }) {
  const { workspace, draft, error, busy, confirming, awaitingReceipt, replacement, expired } = replay.state
  const [tab, setTab] = useState<'headers' | 'body'>('headers')
  const [copied, setCopied] = useState(false)
  const container = useRef<HTMLElement>(null)
  const pathInput = useRef<HTMLInputElement>(null)
  const panelID = useId()
  const baseline = workspace?.baseline
  const baselineSequence = baseline?.sequence
  const baselineStartedAt = baseline?.startedAt
  const running = busy || awaitingReceipt || workspace?.run?.state === 'running'
  const disabled = running || confirming || expired
  useOverlayDismiss({ containerRef: container, initialFocusRef: pathInput, restoreFocusRef, dismissBlocked: false, onDismiss: onClose })
  useEffect(() => { if (baselineSequence !== undefined) pathInput.current?.focus() }, [baselineSequence, baselineStartedAt])
  const candidates = environments.filter((environment) => environment.project === workspace?.project)
  const selected = candidates.find((environment) => environment.name === draft?.environment)
  const service = selected?.services.find((candidate) => candidate.name === baseline?.target)
  const binding = selected?.bindings?.find((candidate) => candidate.service === baseline?.target)
  const reviewedDestination = workspace && workspace.destination?.environment === draft?.environment ? workspace.destination : undefined
  const endpoint = service?.endpoints?.find((candidate) => candidate.kind === 'public' && candidate.protocol === 'http')?.url || reviewedDestination?.url
  const provider = binding?.provider || reviewedDestination?.provider || (service?.kind === 'resource' ? 'container' : 'local')
  const destinationReason = baseline && selected ? replayDestinationReason(selected, baseline) : 'Destination environment is unavailable'
  const outdated = !!workspace?.result && !!draft && replay.state.resultFingerprint !== replayDraftFingerprint(draft)
  const stateLabel = awaitingReceipt ? 'Checking the run receipt…' : workspace?.run?.state === 'running' ? 'Sending replay…' : busy ? 'Preparing replay…' : expired ? 'Replay is no longer available. Reopen Replay to continue.' : confirming ? 'Review the remote write before sending.' : workspace?.run?.state === 'failed' || workspace?.run?.state === 'interrupted' ? 'Replay ended. Review the delivery outcome below.' : workspace?.result ? outdated ? 'Result is from the previous request.' : 'Replay complete.' : ''

  return <>
    <aside ref={container} className="traffic-detail traffic-detail--maximized traffic-replay" role="dialog" aria-modal="true" aria-label={`Replay request${baseline ? ` ${baseline.sequence}` : ''}`} tabIndex={-1} onPointerDownCapture={replay.activity} onPointerMoveCapture={replay.activity} onKeyDownCapture={replay.activity} onInputCapture={replay.activity} onScrollCapture={replay.activity}>
      <header className="replay-header">
        <div><span className="eyebrow">HTTP REPLAY</span><h2>Replay request{baseline ? ` #${baseline.sequence}` : ''}</h2>{baseline && <p><code>{baseline.source}</code> → <code>{baseline.target}</code><span>Original: {baseline.project}/{baseline.environment}</span></p>}</div>
        <div className="replay-header__actions"><button type="button" className="icon-button" aria-label="Close replay" onClick={onClose}>×</button></div>
      </header>
      {stateLabel && <div className="replay-status" role="status" aria-live="polite"><span>{stateLabel}</span></div>}
      {error && <div className="replay-error"><ActionErrorNotice error={error} onDismiss={replay.dismissError} /></div>}
      {workspace && baseline && draft && <>
        <form className="replay-destination" onSubmit={(event) => { event.preventDefault(); if (!disabled) void replay.send() }} aria-label="Replay destination and request">
          <div className="replay-destination__scope">
            <label><span>DESTINATION ENVIRONMENT</span><select aria-label="Replay destination environment" value={draft.environment} disabled={disabled} onChange={(event) => { replay.destination(event.target.value); setCopied(false) }}>
              {!candidates.some((candidate) => candidate.name === draft.environment) && <option value={draft.environment} disabled>{draft.environment} — unavailable</option>}
              {candidates.map((environment) => { const reason = replayDestinationReason(environment, baseline); return <option value={environment.name} key={environment.name} disabled={!!reason}>{environment.project}/{environment.name}{reason ? ` — ${reason}` : ''}</option> })}
            </select></label>
            <div className="replay-provider"><span>PROVIDER</span><strong>{provider}{provider === 'remote' ? ` · ${binding?.remote?.classification || reviewedDestination?.classification || 'unknown'} · ${binding?.remote?.writePolicy || reviewedDestination?.writePolicy || ''}` : ''}</strong>{endpoint && <div><a href={endpoint} target="_blank" rel="noreferrer">{endpoint}</a><button type="button" className="traffic-copy-button" aria-label="Copy replay endpoint" onClick={() => void navigator.clipboard.writeText(endpoint).then(() => setCopied(true)).catch(() => setCopied(false))}>{copied ? 'COPIED' : 'COPY'}</button></div>}</div>
          </div>
          <div className="replay-request-line"><label><span>METHOD</span><select aria-label="Replay method" value={draft.method} disabled={disabled} onChange={(event) => replay.change({ ...draft, method: event.target.value })}>{replayMethods.map((method) => <option key={method}>{method}</option>)}</select></label><label><span>PATH AND QUERY</span><input ref={pathInput} aria-label="Replay path and query" value={draft.requestTarget} disabled={disabled} spellCheck={false} autoComplete="off" onChange={(event) => replay.change({ ...draft, requestTarget: event.target.value })} /></label><button type="submit" className="button button--primary" disabled={disabled || !!destinationReason}>{running ? 'SENDING…' : 'SEND REPLAY'}</button></div>
          {!!destinationReason && <p className="replay-notice">{destinationReason}. Portless will not start or change the destination.</p>}
        </form>
        <div className="replay-workspace">
          <section className="replay-request" aria-label="Replay request editor">
            <div className="replay-section-heading"><h3>REQUEST EDITOR</h3><button type="button" className="traffic-copy-button" disabled={disabled} onClick={replay.reset}>RESET REQUEST</button></div>
            <ReplayTabs label="Replay request fields" value={tab} options={[{ value: 'headers', label: 'Headers' }, { value: 'body', label: 'Body' }]} onChange={setTab} panelID={panelID} />
            <div className="replay-request__content" id={panelID} role="tabpanel" aria-label={`Replay request ${tab}`}>
              {!!workspace.limitations?.length && <details className="replay-normalization"><summary>Capture and request preparation notes</summary><ul>{workspace.limitations.map((limitation, index) => <li key={`${limitation.code}-${index}`}>{limitation.message}</li>)}</ul></details>}
              {tab === 'headers' ? <ReplayHeaderEditor rows={draft.headers} disabled={disabled} onChange={(headers) => replay.change({ ...draft, headers })} /> : <div className="replay-body">
                <label><span>BODY SOURCE</span><select aria-label="Replay body source" value={draft.bodyMode} disabled={disabled} onChange={(event) => { const bodyMode = event.target.value as typeof draft.bodyMode; replay.change({ ...draft, bodyMode, body: bodyMode === 'replacement' ? '' : bodyMode === 'captured' ? baseline.requestBody || '' : '' }) }}><option value="captured" disabled={!capturedReplayBodyAvailable(baseline)}>Captured body{capturedReplayBodyAvailable(baseline) ? '' : ' — incomplete or unavailable'}</option><option value="replacement">Replacement text</option><option value="empty">Explicitly empty body</option></select></label>
                {!capturedReplayBodyAvailable(baseline) && <p className="replay-notice" role="note">The complete original body is unavailable. Supply replacement text or explicitly choose an empty body before sending.</p>}
                {draft.bodyMode !== 'empty' && <label className="replay-body__text"><span>{draft.bodyMode === 'captured' ? 'CAPTURED TEXT · READ ONLY' : 'REPLACEMENT TEXT'}</span><textarea aria-label="Replay request body" value={draft.bodyMode === 'captured' ? baseline.requestBody || '' : draft.body} readOnly={draft.bodyMode === 'captured'} disabled={disabled} spellCheck={false} autoComplete="off" onChange={(event) => replay.change({ ...draft, body: event.target.value })} /></label>}
                {draft.bodyMode === 'replacement' && baseline.requestBody && !capturedReplayBodyAvailable(baseline) && <details><summary>Captured prefix for reference</summary><pre>{baseline.requestBody}</pre></details>}
                {draft.bodyMode === 'empty' && <p className="replay-empty">The replay will send an empty body. This choice is an explicit request edit.</p>}
              </div>}
            </div>
          </section>
          <TrafficReplayResult workspace={workspace} outdated={outdated} />
        </div>
      </>}
    </aside>
    {confirming && workspace?.destination && draft && <FormDialog className="replay-confirmation" role="alertdialog" label="Confirm remote replay" closeLabel="Cancel remote replay" onClose={replay.cancelConfirmation} header={<h2>Send this request to the remote service?</h2>}>
      <div className="replay-confirmation__body"><p>This request may change remote application data.</p><dl><div><dt>ENVIRONMENT</dt><dd>{workspace.project}/{workspace.destination.environment}</dd></div><div><dt>SERVICE</dt><dd>{baseline?.source} → {baseline?.target}</dd></div><div><dt>CLASSIFICATION</dt><dd>{workspace.destination.classification}</dd></div><div><dt>REQUEST</dt><dd><code>{draft.method} {draft.requestTarget}</code></dd></div></dl><p>Policy: {workspace.destination.writePolicy}. Confirmation applies only to this prepared request.</p></div><footer><button type="button" className="button button--quiet" onClick={replay.cancelConfirmation}>CANCEL</button><button type="button" className="button button--danger" onClick={() => void replay.confirm()}>SEND REMOTE REPLAY</button></footer>
    </FormDialog>}
    {replacement && <FormDialog className="replay-confirmation" role="alertdialog" label="Replace replay workspace" closeLabel="Keep existing replay" closeBlocked={running} onClose={replay.keep} header={<h2>Replace the current replay?</h2>}><div className="replay-confirmation__body"><p>Keep the current draft and result, or release them and prepare request #{replacement.sequence}. Preparing the new request sends no application traffic.</p></div><footer><button type="button" className="button button--quiet" disabled={running} onClick={replay.keep}>KEEP CURRENT REPLAY</button><button type="button" className="button button--primary" disabled={running} onClick={() => void replay.replace()}>REPLACE WORKSPACE</button></footer></FormDialog>}
  </>
}
