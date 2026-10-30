import type { Messages } from '..'
import { formatDayClock, formatSlept } from './format'

export const sleep = {
  title: 'Sleeping',
  until: (at: string) => `Sleeping until ${formatDayClock(at)}`,
  remaining: (seconds: number) => `${Math.floor(seconds / 60)} min ${seconds % 60} s left`,
  awaitingWake: 'Waiting for the server to schedule the wake',
  instances: 'Sleeping task conversations',
  instance: (id: string) => `Instance ${id}`,
  wakeReference: (id: string) => `Wake ref ${id}`,
  woke: 'Woke up on schedule',
  wokeEarly: 'Woke early',
  slept: (ms: number) => `Slept ${formatSlept(ms)}`,
  idTip: (id: string) => `Sleep ${id}`,
  states: {
    requested: "Sleep requested; this turn hasn't ended yet",
    sleeping: 'Sleeping',
    woken: 'Sleep ended; follow-up work is scheduled',
    cancelled: 'Sleep cancelled',
  },
  endReasons: {
    deadline: 'Woke on schedule',
    queued_work: 'Woken early by new work',
    execution_failed: 'This turn failed',
    interrupted: 'This turn was interrupted',
    task_ended: 'Task ended',
    node_changed: 'Node changed',
    session_reopened: 'New session started',
    owner_deleted: 'Owner deleted',
  },
} satisfies Messages['sleep']
