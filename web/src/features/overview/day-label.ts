import { t } from '@/i18n'

// The day a time falls on: today, yesterday or the date. Used by the items in issues and failures and in "Needs you".
export function dayLabel(time: string): string {
  const date = new Date(time)
  const today = new Date()
  const yesterday = new Date(today.getFullYear(), today.getMonth(), today.getDate() - 1)
  if (date.toDateString() === today.toDateString()) return t.overview.anomalies.today
  if (date.toDateString() === yesterday.toDateString()) return t.overview.anomalies.yesterday
  return t.overview.anomalies.day(time)
}
