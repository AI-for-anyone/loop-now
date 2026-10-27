import { useEffect, useState } from 'react'

// 当前时刻（毫秒），每隔 intervalMs 刷新一次，用于页面上走动的时长。enabled 为 false 时不启动计时器。
export function useNow(intervalMs = 1000, enabled = true): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!enabled) return
    const timer = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(timer)
  }, [intervalMs, enabled])
  return now
}
