import type { Schemas } from '@/api/client'
import { t } from '@/i18n'

// How a background task ended, short: its status, and the exit code when there is one.
export function endSummary(task: Pick<Schemas['BackgroundTask'], 'status' | 'exitCode'>): string {
  const status = t.background.card.status[task.status]
  return task.exitCode !== null ? `${status} · ${t.background.card.exitCode(task.exitCode)}` : status
}

type Entry = Schemas['TimelineEntry']

// The background tasks of one reply as its record tells them (a task node's replies carry no list of their own): each
// background_task_started, with its background_task_ended when there is one; the node's live list adds whether one was asked to stop.
// A task with no end in the loaded entries is running, unless it can't be: the reply has ended (a node's turn waits for its tasks), or
// the node's list is known and doesn't have it. Its end is then on a page of the record not loaded yet, and its tool stays a chip.
export function tasksFromTimeline(entries: Entry[], runId: string, running: Schemas['BackgroundTask'][] | undefined, runEnded: boolean): Schemas['BackgroundTask'][] {
  return entries.flatMap((entry) => {
    if (entry.type !== 'background_task_started') return []
    const end = entries.find((other): other is Extract<Entry, { type: 'background_task_ended' }> => other.type === 'background_task_ended' && other.taskId === entry.taskId)
    const live = running?.find((task) => task.id === entry.taskId)
    if (!end && (runEnded || (running !== undefined && !live))) return []
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

// How far a background task has got: running, asked to stop, ended. It only ever moves forward.
const progress = (task: Schemas['BackgroundTask']) => (task.status !== 'running' ? 2 : task.stopRequestedAt ? 1 : 0)

// The background tasks of a run whose copies (history, events, the live state) arrived in no fixed order: each task as far as any copy
// has seen it, in the order they started. Needed because an ended run still changes (its tasks end later), so the copy that wins by run
// state alone may hold an older task list.
export function latestBackgroundTasks(lists: Schemas['BackgroundTask'][][]): Schemas['BackgroundTask'][] {
  const byId = new Map<string, Schemas['BackgroundTask']>()
  for (const list of lists) {
    for (const task of list) {
      const known = byId.get(task.id)
      if (!known || progress(task) > progress(known)) byId.set(task.id, task)
    }
  }
  return [...byId.values()].sort((a, b) => Date.parse(a.startedAt) - Date.parse(b.startedAt))
}
