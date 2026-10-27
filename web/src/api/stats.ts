import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { scopeQueryKey, type ScopeQuery } from '@/lib/scope'
import { api, call } from './client'

export type StatsRange = 'today' | 'week' | 'month'
export const STATS_RANGES: StatsRange[] = ['today', 'week', 'month']

export const statsKeys = {
  report: (range: StatsRange, scope: string) => ['stats', range, scope] as const,
}

// 时间窗按浏览器本地时间：今天从零点起按小时分桶；近 7 天、近 30 天从往前第 6、29 天的零点起按天分桶。都截止到现在。
export function statsWindow(range: StatsRange, now: Date = new Date()) {
  const days = range === 'today' ? 0 : range === 'week' ? 6 : 29
  return {
    from: new Date(now.getFullYear(), now.getMonth(), now.getDate() - days),
    to: now,
    bucket: range === 'today' ? ('hour' as const) : ('day' as const),
  }
}

const pad = (n: number) => String(Math.trunc(n)).padStart(2, '0')

// RFC 3339，带本地时区的偏移，例如 2026-10-27T00:00:00+08:00。
export function localTime(date: Date): string {
  const offset = -date.getTimezoneOffset()
  const sign = offset >= 0 ? '+' : '-'
  return (
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}` +
    `${sign}${pad(Math.abs(offset) / 60)}:${pad(Math.abs(offset) % 60)}`
  )
}

// 统计报表：时间窗在每次请求时计算（截止时刻随时间推移）。换时间范围或范围时保留上一份报表，新的到达前淡显。
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
