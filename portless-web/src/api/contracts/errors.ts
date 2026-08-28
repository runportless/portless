export interface APIErrorShape {
  code: string
  message: string
  subject?: Record<string, unknown>
  details?: Record<string, unknown>
  remediation?: Array<{ label: string; command?: string; url?: string }>
}
