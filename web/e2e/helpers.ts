import { readFile } from 'node:fs/promises'
import http from 'node:http'
import { expect, type Page } from '@playwright/test'
import { readE2EState } from './state'

export async function issueBrowserClaim(next = environmentPath()) {
  const state = readE2EState()
  const response = await fetch(`${state.baseURL}/api/v1/browser-claims`, {
    method: 'POST',
    headers: {
      Accept: 'application/json',
      Authorization: `Bearer ${state.token}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ next }),
  })
  if (!response.ok) throw new Error(`issue browser claim: ${response.status} ${await response.text()}`)
  const value = await response.json() as { url: string }
  return `${state.baseURL}${new URL(value.url).pathname}`
}

export async function authenticate(page: Page, next = environmentPath()) {
  await page.goto(await issueBrowserClaim(next))
  await expect(page).toHaveURL(new RegExp(`${escapeRegExp(next)}$`))
  await expect(page.getByRole('heading', { name: next === '/projects' ? 'Projects' : readE2EState().environment, exact: true })).toBeVisible()
}

export function environmentPath(tab?: string) {
  const state = readE2EState()
  const base = `/environments/${encodeURIComponent(state.project)}/${encodeURIComponent(state.environment)}`
  return tab && tab !== 'overview' ? `${base}?tab=${tab}` : base
}

export async function controlAPI<T>(path: string, init: RequestInit = {}): Promise<T> {
  const state = readE2EState()
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  headers.set('Authorization', `Bearer ${state.token}`)
  const response = await fetch(`${state.baseURL}${path}`, { ...init, headers })
  if (!response.ok) throw new Error(`${init.method || 'GET'} ${path}: ${response.status} ${await response.text()}`)
  return await response.json() as T
}

export async function applicationRequest(path: string, headers: Record<string, string> = {}) {
  const state = readE2EState()
  const endpoint = new URL(state.baseURL)
  return await new Promise<{ status: number; body: string; headers: http.IncomingHttpHeaders }>((resolve, reject) => {
    const request = http.request({
      hostname: endpoint.hostname,
      port: Number(endpoint.port),
      method: 'GET',
      path,
      headers: { Host: state.applicationHost, ...headers },
      timeout: 10_000,
    }, (response) => {
      const chunks: Buffer[] = []
      response.on('data', (chunk) => chunks.push(Buffer.from(chunk)))
      response.on('end', () => resolve({ status: response.statusCode || 0, body: Buffer.concat(chunks).toString('utf8'), headers: response.headers }))
    })
    request.on('timeout', () => request.destroy(new Error('application request timed out')))
    request.on('error', reject)
    request.end()
  })
}

export async function readDownload(downloadPath: string | null) {
  if (!downloadPath) throw new Error('Playwright did not retain the downloaded file')
  return await readFile(downloadPath, 'utf8')
}

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}
