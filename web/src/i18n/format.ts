// 各语言文案表共用的格式化：日期、时间、数字与复数按传入的 locale 交给 Intl，文案表各自传入自己的 locale。
// 时长等需要文字单位的格式在各语言的文案表里。

// 时间（时:分，可带秒），24 小时制。用 hourCycle h23 而不是 hour12: false：后者在部分浏览器的 en-US 下把零点写成 24 点。
export function clock(locale: string, time: string, seconds = true): string {
  return new Date(time).toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit', ...(seconds ? { second: '2-digit' } : {}), hourCycle: 'h23' })
}

// 月与日。
export function monthDay(locale: string, time: string): string {
  return new Date(time).toLocaleDateString(locale, { month: 'numeric', day: 'numeric' })
}

// 月、日加时分，24 小时制。
export function monthDayClock(locale: string, time: string): string {
  return new Date(time).toLocaleString(locale, { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
}

// 日期加时间，24 小时制。
export function dateTime(locale: string, time: string): string {
  return new Date(time).toLocaleString(locale, { hourCycle: 'h23' })
}

// 带千位分隔的数。
export function grouped(locale: string, value: number): string {
  return value.toLocaleString(locale)
}

// 简写的大数（15K、2.5M），最多一位小数。
export function compact(locale: string, value: number): string {
  return value.toLocaleString(locale, { notation: 'compact', maximumFractionDigits: 1 })
}

// 按数量选复数形式；没有对应形式时用 other。
export function plural(locale: string, count: number, forms: Partial<Record<Intl.LDMLPluralRule, string>> & { other: string }): string {
  return forms[new Intl.PluralRules(locale).select(count)] ?? forms.other
}

// 运行记录中每一步的时间，精确到毫秒；与语言无关。
export function preciseClock(time: string): string {
  const date = new Date(time)
  const pad = (value: number, size = 2) => String(value).padStart(size, '0')
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}`
}

// 字节数，二进制单位；与语言无关。
export function bytes(size: number): string {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KiB`
  return `${(size / 1024 / 1024).toFixed(1)} MiB`
}
