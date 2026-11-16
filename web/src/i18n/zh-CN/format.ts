import { bytes, clock, dateTime, grouped, monthDay, monthDayClock, preciseClock } from '../format'

const locale = 'zh-CN'

// Time (hours:minutes, optionally with seconds), used for the latest change and the start time on the overview.
export function formatClock(time: string, seconds = true): string {
  return clock(locale, time, seconds)
}

// Today's times show only hours and minutes; earlier ones add the date.
export function formatDayClock(time: string): string {
  const date = new Date(time)
  const hm = formatClock(time, false)
  return date.toDateString() === new Date().toDateString() ? hm : `${monthDay(locale, time)} ${hm}`
}

// Time of each step in the run log, to the millisecond.
export const formatPreciseClock = preciseClock

export function formatStepDuration(ms: number): string {
  if (ms < 1000) return `${Math.max(0, Math.round(ms))} ms`
  if (ms < 100_000) return `${(ms / 1000).toFixed(1)} s`
  const minutes = Math.floor(ms / 60_000)
  return `${minutes} 分 ${Math.round((ms % 60_000) / 1000)} 秒`
}

// sleep duration: the wake-up message arrives a few seconds after the timer, so round to whole
// minutes; under 1 minute it is given in seconds, longer durations in hours and minutes.
export function formatSlept(ms: number): string {
  const total = Math.max(0, Math.round(ms / 1000))
  if (total < 60) return `${total} 秒`
  const minutes = Math.round(total / 60)
  if (minutes < 60) return `${minutes} 分钟`
  return minutes % 60 ? `${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分` : `${minutes / 60} 小时`
}

// Run duration and time used so far in this turn: under 1 minute in seconds,
// under 1 hour in minutes and seconds, longer in hours and minutes.
export function formatElapsed(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  if (total < 60) return `${total} 秒`
  const minutes = Math.floor(total / 60)
  if (minutes < 60) return `${minutes} 分 ${total % 60} 秒`
  return `${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分`
}

export const formatBytes = bytes

export function formatMonthDay(time: string): string {
  return monthDay(locale, time)
}

// Time above messages and replies: month and day plus hours and minutes.
export function formatMonthDayClock(time: string): string {
  return monthDayClock(locale, time)
}

export function formatCount(value: number): string {
  return grouped(locale, value)
}

export function formatTime(time: string): string {
  return dateTime(locale, time)
}
