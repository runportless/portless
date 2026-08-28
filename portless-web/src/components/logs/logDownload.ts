export const rawLogURLLifetimeMilliseconds = 60_000

type LogDownloadEnvironment = {
  createObjectURL: (blob: Blob) => string
  open: (url: string, target: string, features: string) => unknown
  revokeObjectURL: (url: string) => void
  setTimeout: (callback: () => void, milliseconds: number) => unknown
}

export function logBlob(content: string) {
  return new Blob([content], { type: 'text/plain;charset=utf-8' })
}

export function openRawLog(content: string, environment: LogDownloadEnvironment = browserLogDownloadEnvironment()) {
  const url = environment.createObjectURL(logBlob(content))
  environment.open(url, '_blank', 'noopener,noreferrer')
  environment.setTimeout(() => environment.revokeObjectURL(url), rawLogURLLifetimeMilliseconds)
}

function browserLogDownloadEnvironment(): LogDownloadEnvironment {
  return {
    createObjectURL: (blob) => window.URL.createObjectURL(blob),
    open: (url, target, features) => window.open(url, target, features),
    revokeObjectURL: (url) => window.URL.revokeObjectURL(url),
    setTimeout: (callback, milliseconds) => window.setTimeout(callback, milliseconds),
  }
}
