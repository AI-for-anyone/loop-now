import { useEffect, useState } from 'react'

// The current time (ms), refreshed every intervalMs, for durations that tick on the page. No timer is started when enabled is false.
export function useNow(intervalMs = 1000, enabled = true): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!enabled) return
    const timer = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(timer)
  }, [intervalMs, enabled])
  return now
}
