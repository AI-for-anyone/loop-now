import { bytes, clock, compact, dateTime, grouped, monthDay, monthDayClock, preciseClock } from '../format'

const locale = 'en-US'

// 时间（时:分，可带秒），24 小时制。
export function formatClock(time: string, seconds = true): string {
  return clock(locale, time, seconds)
}

// 今天的只写时分，更早的带上日期。
export function formatDayClock(time: string): string {
  const date = new Date(time)
  const hm = formatClock(time, false)
  return date.toDateString() === new Date().toDateString() ? hm : `${monthDay(locale, time)} ${hm}`
}

// 运行记录中每一步的时间，精确到毫秒。
export const formatPreciseClock = preciseClock

export function formatStepDuration(ms: number): string {
  if (ms < 1000) return `${Math.max(0, Math.round(ms))} ms`
  if (ms < 100_000) return `${(ms / 1000).toFixed(1)} s`
  const minutes = Math.floor(ms / 60_000)
  return `${minutes} min ${Math.round((ms % 60_000) / 1000)} s`
}

// sleep 时长：按分钟取整；不足 1 分钟到秒，更长的到小时和分钟。
export function formatSlept(ms: number): string {
  const total = Math.max(0, Math.round(ms / 1000))
  if (total < 60) return `${total} s`
  const minutes = Math.round(total / 60)
  if (minutes < 60) return `${minutes} min`
  return minutes % 60 ? `${Math.floor(minutes / 60)} h ${minutes % 60} min` : `${minutes / 60} h`
}

// 运行时长与本轮已用时间：不足 1 分钟到秒，不足 1 小时到分秒，更长的到小时和分钟。
export function formatElapsed(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  if (total < 60) return `${total} s`
  const minutes = Math.floor(total / 60)
  if (minutes < 60) return `${minutes} min ${total % 60} s`
  return `${Math.floor(minutes / 60)} h ${minutes % 60} min`
}

export const formatBytes = bytes

export function formatMonthDay(time: string): string {
  return monthDay(locale, time)
}

// 消息与回复上方的时间：月、日加时分。
export function formatMonthDayClock(time: string): string {
  return monthDayClock(locale, time)
}

export function formatCount(value: number): string {
  return grouped(locale, value)
}

// token 等大数：15K、2.5M。
export function formatCompact(value: number): string {
  return compact(locale, value)
}

export function formatTime(time: string): string {
  return dateTime(locale, time)
}
