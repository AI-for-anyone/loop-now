import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { scopeQueryKey, type ScopeQuery } from '@/lib/scope'
import { api, call } from './client'

export type StatsRange = 'today' | 'week' | 'month'
export const STATS_RANGES: StatsRange[] = ['today', 'week', 'month']

export const statsKeys = {
  report: (range: StatsRange, scope: string) => ['stats', range, scope] as const,
}

// Time windows use the browser's local time: today is bucketed by hour from midnight; the last 7 and last 30 days are bucketed by day from
// midnight 6 and 29 days back. All of them end now.
export function statsWindow(range: StatsRange, now: Date = new Date()) {
  const days = range === 'today' ? 0 : range === 'week' ? 6 : 29
  return {
    from: new Date(now.getFullYear(), now.getMonth(), now.getDate() - days),
    to: now,
    bucket: range === 'today' ? ('hour' as const) : ('day' as const),
  }
}

const pad = (n: number) => String(Math.trunc(n)).padStart(2, '0')

// RFC 3339 with the local time zone offset, e.g. 2026-10-27T00:00:00+08:00.
export function localTime(date: Date): string {
  const offset = -date.getTimezoneOffset()
  const sign = offset >= 0 ? '+' : '-'
  return (
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}` +
    `${sign}${pad(Math.abs(offset) / 60)}:${pad(Math.abs(offset) % 60)}`
  )
}

// The stats report: the time window is computed on every request (the end moves with time). When the time range or the scope changes, the
// previous report is kept and shown faded until the new one arrives.
export function useStats(range: StatsRange, scope: ScopeQuery) {
  return useQuery({
    queryKey: statsKeys.report(range, scopeQueryKey(scope)),
    queryFn: () => {
      const { from, to, bucket } = statsWindow(range)
      return call(api.GET('/stats', { params: { query: { from: localTime(from), to: localTime(to), bucket, ...scope } } }))
    },
    placeholderData: keepPreviousData,
    refetchOnWindowFocus: true,
  })
}
