import type { Schemas } from '@/api/client'
import { t } from '@/i18n'

// How a background task ended, short: its status, and the exit code when there is one.
export function endSummary(task: Pick<Schemas['BackgroundTask'], 'status' | 'exitCode'>): string {
  const status = t.background.card.status[task.status]
  return task.exitCode !== null ? `${status} · ${t.background.card.exitCode(task.exitCode)}` : status
}
