// Formatting shared by every language's copy table: dates, times, numbers and plurals are handed to Intl with the locale passed in,
// and each copy table passes its own locale. Formats that need words for units, such as durations, live in each language's copy table.

// Time (hours:minutes, optionally with seconds), 24-hour clock. Uses hourCycle h23 rather
// than hour12: false, because in some browsers the latter writes midnight as 24 under en-US.
export function clock(locale: string, time: string, seconds = true): string {
  return new Date(time).toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit', ...(seconds ? { second: '2-digit' } : {}), hourCycle: 'h23' })
}

// Month and day.
export function monthDay(locale: string, time: string): string {
  return new Date(time).toLocaleDateString(locale, { month: 'numeric', day: 'numeric' })
}

// Month and day plus hours and minutes, 24-hour clock.
export function monthDayClock(locale: string, time: string): string {
  return new Date(time).toLocaleString(locale, { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
}

// Date plus time, 24-hour clock.
export function dateTime(locale: string, time: string): string {
  return new Date(time).toLocaleString(locale, { hourCycle: 'h23' })
}

// Number with thousands separators.
export function grouped(locale: string, value: number): string {
  return value.toLocaleString(locale)
}

// Abbreviated large number (15K, 2.5M), at most one decimal place.
export function compact(locale: string, value: number): string {
  return value.toLocaleString(locale, { notation: 'compact', maximumFractionDigits: 1 })
}

// Picks the plural form for the count; uses other when there is no matching form.
export function plural(locale: string, count: number, forms: Partial<Record<Intl.LDMLPluralRule, string>> & { other: string }): string {
  return forms[new Intl.PluralRules(locale).select(count)] ?? forms.other
}

// Time of each step in the run log, to the millisecond; independent of language.
export function preciseClock(time: string): string {
  const date = new Date(time)
  const pad = (value: number, size = 2) => String(value).padStart(size, '0')
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}`
}

// Byte count in binary units; independent of language.
export function bytes(size: number): string {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KiB`
  return `${(size / 1024 / 1024).toFixed(1)} MiB`
}
