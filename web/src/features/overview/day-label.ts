import { t } from '@/i18n'

// 时间所在的日子：今天、昨天或日期。异常与失败、待你处理的条目都用它。
export function dayLabel(time: string): string {
  const date = new Date(time)
  const today = new Date()
  const yesterday = new Date(today.getFullYear(), today.getMonth(), today.getDate() - 1)
  if (date.toDateString() === today.toDateString()) return t.overview.anomalies.today
  if (date.toDateString() === yesterday.toDateString()) return t.overview.anomalies.yesterday
  return t.overview.anomalies.day(time)
}
