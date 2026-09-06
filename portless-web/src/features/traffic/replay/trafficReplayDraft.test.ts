import { describe, expect, it } from 'vitest'
import type { TrafficExchange } from '../../../api/contracts/traffic'
import type { TrafficReplayDraft } from '../../../api/contracts/traffic_replay'
import { capturedReplayBodyAvailable, clearReplayCredentials, replayEntryReason, replayRequestDraft, serializeReplayDraft } from './trafficReplayDraft'
import { replayResponseText } from './TrafficReplayResult'

const baseline = {
  protocol: 'http', method: 'POST', requestBody: '{"id":9007199254740993}',
  requestCapture: { state: 'complete', observedBytes: 23, capturedBytes: 23, encoding: 'identity', exact: true },
} as TrafficExchange
const request: TrafficReplayDraft = { environment: 'local', method: 'POST', requestTarget: '/a%2Fb?tag=a&tag=b&space=+&other=%20&empty=', headers: { 'X-Tag': ['a', 'b'] }, bodyMode: 'captured', body: baseline.requestBody! }

describe('HTTP replay drafts', () => {
  it('preserves raw target spelling, repeated headers, and complete body bytes', () => {
    const result = serializeReplayDraft(replayRequestDraft(request), baseline)
    expect(result.requestTarget).toBe(request.requestTarget)
    expect(result.headers).toEqual({ 'X-Tag': ['a', 'b'] })
    expect(result.body).toBe('{"id":9007199254740993}')
  })

  it('combines case-insensitive header names without collapsing repeated values', () => {
    const draft = replayRequestDraft({ ...request, headers: { 'X-Tag': ['a'], 'x-tag': ['b'] } })
    expect(serializeReplayDraft(draft, baseline).headers).toEqual({ 'X-Tag': ['a', 'b'] })
  })

  it.each(['truncated', 'omitted', 'unsupported', 'incomplete'] as const)('requires an explicit replacement or empty decision for %s captures', (state) => {
    const incomplete = { ...baseline, requestCapture: { ...baseline.requestCapture!, state } }
    const draft = replayRequestDraft(request)
    expect(() => serializeReplayDraft(draft, incomplete)).toThrow('complete request body')
    expect(serializeReplayDraft({ ...draft, bodyMode: 'replacement', body: 'new complete body' }, incomplete).body).toBe('new complete body')
    expect(serializeReplayDraft({ ...draft, bodyMode: 'empty' }, incomplete).body).toBe('')
  })

  it('does not infer completeness from a missing body or false truncation flag', () => {
    expect(capturedReplayBodyAvailable({ ...baseline, requestBody: '', requestCapture: undefined, requestBodyTruncated: false })).toBeFalsy()
    expect(capturedReplayBodyAvailable({ ...baseline, requestCapture: { ...baseline.requestCapture!, encoding: 'gzip' } })).toBe(false)
  })

  it('requires explicit handling of redacted credentials and never sends a placeholder', () => {
    const draft = replayRequestDraft({ ...request, headers: { Authorization: ['[REDACTED]'] } })
    expect(draft.headers[0]).toMatchObject({ sensitive: true, value: '' })
    expect(() => serializeReplayDraft(draft, baseline)).toThrow('Provide Authorization')
    const omitted = serializeReplayDraft({ ...draft, headers: draft.headers.map((row) => ({ ...row, omitted: true })) }, baseline)
    expect(omitted.omittedHeaders).toEqual(['Authorization'])
    expect(omitted.headers).toEqual({})
  })

  it('clears manually supplied credential values while retaining the rest of the draft', () => {
    const draft = replayRequestDraft({ ...request, headers: { Authorization: ['Bearer private'], 'X-Tag': ['a', 'b'] } })
    const cleared = clearReplayCredentials(draft)
    expect(cleared.headers.map((row) => row.value)).toEqual(['', 'a', 'b'])
    expect(draft.headers[0].value).toBe('Bearer private')
  })

  it('clears credentials echoed into body, raw target, and ordinary headers before discarding credential rows', () => {
    const draft = replayRequestDraft({ ...request, requestTarget: '/echo?token=access-secret&session=session-secret', bodyMode: 'replacement', body: 'Bearer access-secret; session-secret; csrf-secret; api-secret', headers: {
      Authorization: ['Bearer access-secret'], Cookie: ['session=session-secret; csrf=csrf-secret'], 'X-Api-Key': ['api-secret'], 'X-Echo': ['access-secret session-secret csrf-secret api-secret'],
    } })
    const cleared = clearReplayCredentials(draft)
    expect(cleared.requestTarget).toBe('/echo?token=[REDACTED]&session=[REDACTED]')
    expect(cleared.body).toBe('[REDACTED]; [REDACTED]; [REDACTED]; [REDACTED]')
    expect(cleared.headers.map((row) => row.value)).toEqual(['', '', '', '[REDACTED] [REDACTED] [REDACTED] [REDACTED]'])
    expect(draft.body).toContain('access-secret')
  })

  it('scrubs an earlier draft with newly supplied credentials without restoring earlier redactions', () => {
    const original = replayRequestDraft({ ...request, requestTarget: '/echo?first=first-secret&second=second-secret', body: 'first-secret second-secret', headers: { 'X-Echo': ['first-secret second-secret'] } })
    const first = replayRequestDraft({ ...request, headers: { Authorization: ['Bearer first-secret'] } })
    const second = replayRequestDraft({ ...request, headers: { Cookie: ['session=second-secret'] } })
    const cleared = clearReplayCredentials(clearReplayCredentials(original, first), second)
    expect(cleared.requestTarget).toBe('/echo?first=[REDACTED]&second=[REDACTED]')
    expect(cleared.body).toBe('[REDACTED] [REDACTED]')
    expect(cleared.headers[0].value).toBe('[REDACTED] [REDACTED]')
  })

  it.each(['https://remote.test/a', '//remote.test/a', '/a#fragment', '/a b', '/a%Q0', '/a\nheader'])('rejects unsafe request target %j before preparation', (requestTarget) => {
    expect(() => serializeReplayDraft(replayRequestDraft({ ...request, requestTarget }), baseline)).toThrow('escaped path')
  })

  it('bounds replacement body bytes rather than JavaScript character count', () => {
    const body = 'é'.repeat(25 * 1024 * 1024 / 2)
    expect(serializeReplayDraft(replayRequestDraft({ ...request, bodyMode: 'replacement', body }), baseline).body.length).toBe(body.length)
    expect(() => serializeReplayDraft(replayRequestDraft({ ...request, bodyMode: 'replacement', body: body + 'x' }), baseline)).toThrow('The request body is too large. Reduce its size and try again.')
  })

  it('offers replay only for ordinary supported HTTP methods', () => {
    expect(replayEntryReason(baseline)).toBe('')
    expect(replayEntryReason({ ...baseline, protocol: 'tcp' })).toBeTruthy()
    expect(replayEntryReason({ ...baseline, method: 'CONNECT' })).toBeTruthy()
    expect(replayEntryReason({ ...baseline, method: 'GET', status: 101 })).toBeTruthy()
  })

  it.each<Record<string, string[]>>([
    { 'Content-Type': ['application/grpc+proto'] },
    { 'content-type': ['Text/Event-Stream; charset=utf-8'] },
    { ACCEPT: ['application/json, text/event-stream; q=0.9'] },
    { Accept: ['application/grpc-web+proto'] },
  ])('blocks known streaming captures and edited streaming headers %j', (headers) => {
    expect(replayEntryReason({ ...baseline, requestHeaders: headers })).toContain('not supported')
    expect(replayEntryReason({ ...baseline, responseHeaders: headers })).toContain('not supported')
    expect(() => serializeReplayDraft(replayRequestDraft({ ...request, headers }), baseline)).toThrow('not supported')
    const omitted = replayRequestDraft({ ...request, headers, omittedHeaders: Object.keys(headers) })
    expect(serializeReplayDraft(omitted, baseline).headers).toEqual({})
  })

  it('copies original body bytes and includes status and repeated headers in Raw', () => {
    const text = ' {"id":9007199254740993,"x":1,"x":2,"n":1.00}\n'
    expect(replayResponseText({ ...baseline, responseBody: text }, 'body')).toBe(text)
    expect(replayResponseText({ ...baseline, responseHeaders: { 'Set-Cookie': ['a=1', 'b=2'] } }, 'headers')).toBe('Set-Cookie: a=1\nSet-Cookie: b=2')
    expect(replayResponseText({ ...baseline, status: 201, responseHeaders: { 'X-Tag': ['a', 'b'] }, responseBody: text }, 'raw')).toBe(`HTTP 201\nX-Tag: a\nX-Tag: b\n\n${text}`)
  })
})
