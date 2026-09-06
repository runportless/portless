// One request can run at a time. Changes during it produce one follow-up,
// with the owner reading its newest desired revision when that follow-up starts.
export function createTrafficRefresh(run: () => Promise<void>, delay = 50) {
  let pending = false
  let running = false
  let disposed = false
  let timer: ReturnType<typeof setTimeout> | undefined

  const finish = () => {
    running = false
    if (pending && !disposed) request()
  }
  const request = (immediate = false) => {
    if (disposed) return
    pending = true
    if (running) return
    if (timer !== undefined) {
      if (!immediate) return
      clearTimeout(timer)
    }
    timer = setTimeout(() => {
      timer = undefined
      pending = false
      running = true
      // Owners present request failures; either outcome must release the queue.
      void run().then(finish, finish)
    }, immediate ? 0 : delay)
  }
  return {
    request,
    dispose() {
      disposed = true
      pending = false
      clearTimeout(timer)
    },
  }
}
