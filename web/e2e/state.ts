import { readFileSync } from 'node:fs'

export interface E2EState {
  root: string
  home: string
  checkout: string
  binary: string
  baseURL: string
  token: string
  project: string
  environment: string
  applicationHost: string
}

export function stateFile() {
  const value = process.env.PORTLESS_UI_E2E_STATE_FILE
  if (!value) throw new Error('PORTLESS_UI_E2E_STATE_FILE is not configured')
  return value
}

export function readE2EState(): E2EState {
  return JSON.parse(readFileSync(stateFile(), 'utf8')) as E2EState
}
