import { useEffect, useRef, useState } from 'react'

export const logPollMilliseconds = 1_000

export function startLogTailPolling<T>({ load, onLoad, onError, pollMilliseconds = logPollMilliseconds }: {
  load: (signal: AbortSignal) => Promise<T>
  onLoad: (value: T) => void
  onError: (error: unknown) => void
  pollMilliseconds?: number
}) {
  let active = true
  let nextPoll: ReturnType<typeof setTimeout> | undefined
  const controller = new AbortController()

  const poll = async () => {
    try {
      const value = await load(controller.signal)
      if (active) onLoad(value)
    } catch (error) {
      if (active && !controller.signal.aborted) onError(error)
    } finally {
      if (active) nextPoll = setTimeout(() => void poll(), pollMilliseconds)
    }
  }

  void poll()
  return () => {
    active = false
    controller.abort()
    if (nextPoll !== undefined) clearTimeout(nextPoll)
  }
}

export function useLogTail<T, E>({ identity, initialValue, load, mapError, equal, pollMilliseconds = logPollMilliseconds }: {
  identity: string
  initialValue: () => T
  load: (signal: AbortSignal) => Promise<T>
  mapError: (error: unknown) => E
  equal?: (current: T, next: T) => boolean
  pollMilliseconds?: number
}) {
  const [value, setValue] = useState<T>(() => initialValue())
  const [loaded, setLoaded] = useState(false)
  const [tailing, setTailing] = useState(true)
  const [error, setError] = useState<E | null>(null)
  const [scrollRevision, setScrollRevision] = useState(0)
  const valueRef = useRef(value)
  const identityRef = useRef(identity)
  const initialValueRef = useRef(initialValue)
  const loadRef = useRef(load)
  const mapErrorRef = useRef(mapError)
  const equalRef = useRef(equal)
  initialValueRef.current = initialValue
  loadRef.current = load
  mapErrorRef.current = mapError
  equalRef.current = equal

  useEffect(() => {
    if (identityRef.current === identity) return
    identityRef.current = identity
    const initial = initialValueRef.current()
    valueRef.current = initial
    setValue(initial)
    setLoaded(false)
    setError(null)
  }, [identity])

  useEffect(() => {
    if (!tailing) return
    return startLogTailPolling({
      pollMilliseconds,
      load: (signal) => loadRef.current(signal),
      onLoad: (next) => {
        const unchanged = equalRef.current?.(valueRef.current, next) ?? false
        if (!unchanged) {
          valueRef.current = next
          setValue(next)
          setScrollRevision((revision) => revision + 1)
        }
        setLoaded(true)
        setError(null)
      },
      onError: (reason) => {
        setLoaded(true)
        setError(mapErrorRef.current(reason))
      },
    })
  }, [identity, pollMilliseconds, tailing])

  return {
    value,
    loaded,
    tailing,
    error,
    scrollRevision,
    dismissError: () => setError(null),
    toggleTailing: () => setTailing((current) => !current),
  }
}
