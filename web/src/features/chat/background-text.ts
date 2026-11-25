import type { Schemas } from '@/api/client'
import { t } from '@/i18n'

// How a background task ended, short: its status, and the exit code when there is one.
export function endSummary(task: Pick<Schemas['BackgroundTask'], 'status' | 'exitCode'>): string {
  const status = t.background.card.status[task.status]
  return task.exitCode !== null ? `${status} · ${t.background.card.exitCode(task.exitCode)}` : status
}

type Entry = Schemas['TimelineEntry']

// The background tasks of one reply as its record tells them (a task node's replies carry no list of their own): each
// background_task_started, with its background_task_ended when there is one. A task not ended yet is running; the node's live list adds
// whether it was asked to stop.
export function tasksFromTimeline(entries: Entry[], runId: string, running: Schemas['BackgroundTask'][] = []): Schemas['BackgroundTask'][] {
  return entries.flatMap((entry) => {
    if (entry.type !== 'background_task_started') return []
    const end = entries.find((other): other is Extract<Entry, { type: 'background_task_ended' }> => other.type === 'background_task_ended' && other.taskId === entry.taskId)
    const live = running.find((task) => task.id === entry.taskId)
    return [{
      id: entry.taskId,
      runtime: live?.runtime ?? '',
      kind: entry.kind,
      label: entry.label,
      runId,
      toolCallId: entry.toolCallId,
      startedAt: live?.startedAt ?? entry.at,
      status: end?.status ?? 'running',
      endedAt: end?.at ?? null,
      exitCode: end?.exitCode ?? null,
      stopReason: end?.stopReason ?? null,
      stopRequestedAt: end ? null : (live?.stopRequestedAt ?? null),
      output: end?.output ?? null,
    }]
  })
}
