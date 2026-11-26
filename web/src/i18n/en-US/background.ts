import type { components } from '@/api/schema.gen'

type S = components['schemas']
type Task = S['BackgroundTask']

const plural = (count: number, one: string, many: string) => `${count} ${count === 1 ? one : many}`

// Background tasks: work a CLI started in the background that keeps running after its reply ended (a build, a test run, a dev
// server, a sub-agent).
export const background = {
  mark: (count: number) => `Background ${count}`,
  markLabel: (count: number) => `${plural(count, 'background task is', 'background tasks are')} running; open to see or stop them`,
  title: 'Background tasks',
  running: (count: number) => `${count} running`,
  hint: 'The reply has ended; these are still going',
  nodeHint: 'This step finishes only once they end; stopping one does not stop the step',
  kind: { command: 'Command', subagent: 'Sub-agent', other: 'Task' } satisfies Record<Task['kind'], string>,
  fromReply: (number: number) => `reply #${number}`,
  fromReplyAt: (time: string) => `the reply at ${time}`,
  fromReplyLabel: (reply: string) => `Go to ${reply}`,
  elapsedLabel: (clock: string) => `Running for ${clock}`,
  stop: 'Stop',
  stopLabel: (label: string) => `Stop background task: ${label}`,
  stopping: 'Stopping…',
  retryStop: 'Try again',
  stopAll: (count: number) => `Stop all (${count})`,
  stopAllHint: 'The reply that started each one will say it was stopped.',
  ended: 'It has already ended',
  allEnded: 'All background tasks have ended.',
  card: {
    label: 'Background task',
    // The chip's name: the tool (or the kind), how it stands, and what it runs.
    chipLabel: (tool: string, status: string, label: string) => `${tool} ${status}: ${label}`,
    status: { running: 'Running in the background', completed: 'Finished in the background', failed: 'Failed in the background', stopped: 'Stopped', unknown: 'Outcome unknown' } satisfies Record<Task['status'], string>,
    exitCode: (code: number) => `exit code ${code}`,
    took: (duration: string) => `took ${duration}`,
    output: 'Output',
    outputTail: 'Output (the last part)',
    stopReason: {
      person: 'stopped by you',
      reply_stopped: 'stopped with the reply',
      settings_changed: 'stopped when a settings change restarted the CLI',
      runtime_switched: 'stopped when the runtime changed',
      session_reset: 'stopped when the session was reset',
      agent_deleted: 'stopped when the Agent was deleted',
      task_started: 'stopped when a task was started from this Agent',
      shutdown: 'stopped when Loop Now quit',
      task_limit: 'stopped at the waiting limit',
    } satisfies Record<NonNullable<Task['stopReason']>, string>,
    unknownHint: 'The CLI exited unexpectedly or Loop Now restarted, so how it ended is unknown',
  },
  followUp: {
    before: 'After background task “',
    after: (runtime: string) => `” ended, ${runtime} went on:`,
    afterMany: (others: number, runtime: string) => `” and ${plural(others, 'other', 'others')} ended, ${runtime} went on:`,
    unknown: (runtime: string) => `After a background task ended, ${runtime} went on:`,
    ruler: 'After background work',
  },
  listTag: (count: number) => `Bg ${count}`,
  listTitle: (count: number) => `Idle; ${plural(count, 'background task is', 'background tasks are')} still running`,
  restartWarning: (runtime: string, count: number) => `Settings changed: the next message restarts ${runtime} and stops ${plural(count, 'running background task', 'running background tasks')}.`,
  stopWarning: (count: number) => `Stopping or interrupting also stops ${plural(count, 'background task', 'background tasks')}`,
  stopWithTasks: (count: number) => `Stop (also stops ${plural(count, 'background task', 'background tasks')})`,
} as const
