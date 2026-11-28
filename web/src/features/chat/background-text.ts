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

export type ShownBackgroundState = Schemas['BackgroundTask']['status'] | 'stopping'

export interface BackgroundToolGroup {
  key: string
  name: string
  state: ShownBackgroundState
  status: Schemas['BackgroundTask']['status']
  tasks: Schemas['BackgroundTask'][]
}

// A running task with a pending stop request shows as stopping so it stays separate from tasks still running freely.
export function shownBackgroundState(task: Pick<Schemas['BackgroundTask'], 'status' | 'stopRequestedAt'>): ShownBackgroundState {
  return task.status === 'running' && task.stopRequestedAt ? 'stopping' : task.status
}

export function backgroundStatusText(state: ShownBackgroundState): string {
  return state === 'stopping' ? t.background.stopping : t.background.card.status[state]
}

export function backgroundTaskName(
  task: Pick<Schemas['BackgroundTask'], 'toolCallId' | 'kind'>,
  toolNames?: ReadonlyMap<string, string>,
): string {
  return (task.toolCallId ? toolNames?.get(task.toolCallId) : undefined) ?? t.background.kind[task.kind]
}

// Group background tasks that share the same tool name (or kind fallback) and shown state, ordered by earliest start time.
// Tasks inside each group remain ordered by start time as well.
export function groupBackgroundTasks(
  tasks: Schemas['BackgroundTask'][],
  toolNames?: ReadonlyMap<string, string>,
): BackgroundToolGroup[] {
  const ordered = [...tasks].sort((a, b) => Date.parse(a.startedAt) - Date.parse(b.startedAt))
  const groups = new Map<string, BackgroundToolGroup>()
  for (const task of ordered) {
    const name = backgroundTaskName(task, toolNames)
    const state = shownBackgroundState(task)
    const key = `${name}\0${state}`
    const existing = groups.get(key)
    if (existing) existing.tasks.push(task)
    else groups.set(key, { key, name, state, status: task.status, tasks: [task] })
  }
  return [...groups.values()]
}
