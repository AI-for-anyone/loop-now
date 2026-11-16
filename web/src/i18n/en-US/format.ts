import { bytes, clock, compact, dateTime, grouped, monthDay, monthDayClock, preciseClock } from '../format'

const locale = 'en-US'

// Time (hours:minutes, optionally with seconds), 24-hour clock.
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
  return `${minutes} min ${Math.round((ms % 60_000) / 1000)} s`
}

// sleep duration: rounded to whole minutes; under 1 minute it is given in seconds, longer durations in hours and minutes.
export function formatSlept(ms: number): string {
  const total = Math.max(0, Math.round(ms / 1000))
  if (total < 60) return `${total} s`
  const minutes = Math.round(total / 60)
  if (minutes < 60) return `${minutes} min`
  return minutes % 60 ? `${Math.floor(minutes / 60)} h ${minutes % 60} min` : `${minutes / 60} h`
}

// Run duration and time used so far in this turn: under 1 minute in seconds,
// under 1 hour in minutes and seconds, longer in hours and minutes.
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

// Time above messages and replies: month and day plus hours and minutes.
export function formatMonthDayClock(time: string): string {
  return monthDayClock(locale, time)
}

export function formatCount(value: number): string {
  return grouped(locale, value)
}

// Large numbers such as token counts: 15K, 2.5M.
export function formatCompact(value: number): string {
  return compact(locale, value)
}

export function formatTime(time: string): string {
  return dateTime(locale, time)
}
