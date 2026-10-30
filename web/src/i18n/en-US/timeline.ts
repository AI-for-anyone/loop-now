import type { Messages } from '..'
import { plural } from '../format'
import { formatPreciseClock, formatStepDuration } from './format'

const count = (n: number, one: string, other: string) => `${n} ${plural('en-US', n, { one, other })}`

export const timeline = {
  toggle: (duration: string, tokens: string) => `Run log · ${duration} · ${tokens} tokens`,
  toggleRunning: (elapsed: string) => `Run log · ${elapsed} so far`,
  title: 'Run log',
  total: (duration: string) => `Total ${duration}`,
  loading: 'Loading the run log…',
  meta: {
    model: (provider: string, model: string) => `model=${provider} / ${model}`,
    noModel: 'No model calls',
    config: (version: number) => `Config v${version}`,
    whenBusy: {
      idle: 'Sent while idle',
      queue: 'whenBusy=queue (queued)',
      interrupt: 'whenBusy=interrupt (interrupt)',
    },
  },
  step: {
    queued: 'Queued',
    dispatched: 'Dispatched',
    waiting_slot: 'Waiting for a run slot',
    preparing: 'Preparing the environment',
    launching: 'Pi start',
    cli_install: (cli: string) => `Install ${cli}`,
    workspace_sync: 'Sync config and folders',
    pi_started: 'Pi start',
    pi_reused: 'Pi start',
    pi_stopped: 'Pi process stopped',
    pi_exited: 'Pi process exited unexpectedly',
    running: 'Started replying',
    tool: (name: string) => `Tool ${name}`,
    model_call: {
      assistant: 'Model call',
      tool: "The tool's own usage",
      compaction: 'Context compaction usage',
      cache_warm: 'Cache warm-up',
      other: 'Other usage',
    },
    model_retry_started: 'Model auto-retry',
    model_retry_ended: 'Auto-retry ended',
    compaction: 'Context compaction',
    abort_requested: 'Interrupt received',
    stop_requested: 'Stopped by a person',
    promoted: { interrupt: 'Promoted to interrupt', queue: 'Switched to queue' },
    message_cancelled: 'An Inbox message was cancelled',
    session_missing: 'Session file missing',
    ended: {
      completed: 'Completed',
      failed: 'Failed',
      user_interrupt: 'Interrupted',
      server_restart: 'Cut off by a server restart',
    },
  },
  detail: {
    idleDispatch: 'The Agent was idle, so this was dispatched right away.',
    queued: {
      queue: 'The Agent was busy; queued after the other normal messages.',
      interrupt: 'The Agent was busy; interrupts the current reply, ahead of normal queued messages.',
    },
    installOk: (version: string) => `Installed ${version}.`,
    installFailed: (version: string, error: string | null) => (error ? `Failed to install ${version}: ${error}` : `Failed to install ${version}.`),
    syncOk: (n: number) => (n > 0 ? `Synced ${count(n, 'folder source', 'folder sources')}.` : 'Config synced.'),
    syncFailed: (error: string | null) => (error ? `Sync failed: ${error}` : 'Sync failed.'),
    syncSource: {
      cloned: (path: string) => `${path}: cloned`,
      kept: (path: string) => `${path}: repository already exists, left as is`,
      files_synced: (path: string, written: number, deleted: number) => `${path}: files synced (${written} written, ${deleted} deleted)`,
      failed: (path: string, error: string | null) => (error ? `${path}: failed (${error})` : `${path}: failed`),
    },
    piStarted: (pid: number, reason: string) => `Started a new Pi process, PID ${pid} (${reason}).`,
    piStartReason: { no_process: 'no live process', config_changed: 'restart after a config change' } as Record<string, string>,
    piReused: (pid: number) => `Reused the resident Pi process, PID ${pid}.`,
    piStopped: (pid: number, reason: string, uptime: string) => `PID ${pid}, ${reason}, ran for ${uptime}.`,
    piExited: (pid: number, exit: string, uptime: string) => `PID ${pid}, ${exit}, ran for ${uptime}.`,
    modelCall: (model: string, input: string, output: string, cacheRead: string, cacheWrite: string) =>
      `${model} · Input ${input} · Output ${output} · Cache read ${cacheRead} · Cache write ${cacheWrite}`,
    retryStarted: (attempt: number, max: number, delay: string, error: string) => `Attempt ${attempt}/${max}, retrying in ${delay}: ${error}`,
    retryEnded: (success: boolean, attempt: number, error: string | null) =>
      success
        ? `Retry ${attempt} succeeded.`
        : error
          ? `Still failing after ${count(attempt, 'retry', 'retries')}: ${error}`
          : `Still failing after ${count(attempt, 'retry', 'retries')}.`,
    compaction: {
      succeeded: (before: string | null, after: string | null) =>
        before && after ? `Compacted: about ${before} → ${after} tokens.` : 'Compaction done.',
      failed: (error: string | null) => (error ? `Compaction failed: ${error}` : 'Compaction failed.'),
      aborted: 'Compaction aborted.',
    },
    toolRunning: 'Running',
    commandUnavailable: 'Command not recorded',
    sessionFile: (file: string) => `Session file: ${file}`,
    completed: 'The reply finished normally.',
  },
  field: {
    queued: 'queuedAt',
    dispatched: 'startedAt · status=starting',
    phase: (phase: string) => `phase=${phase}`,
    running: 'runningAt · status=running',
    tool: (state: string) => (state === 'succeeded' ? null : `state=${state}`),
    abort: 'abortRequestedAt · status=aborting',
    stop: 'abortRequestedAt · stop=true',
    promoted: (from: string, to: string) => `whenBusy=${from} → ${to}`,
    message: (messageId: string) => `messageId=${messageId}`,
    ended: (status: string) => `endedAt · status=${status}`,
  },
  usage: {
    title: 'Token usage',
    field: 'usage',
    calls: (n: number) => count(n, 'model call', 'model calls'),
  },
  result: {
    completed: 'Completed',
    failed: 'Failed',
    interrupted: 'Interrupted',
    stopped: 'Stopped',
    voided: 'Never ran',
    server_restart: 'Cut off by a server restart',
    errorField: (code: string, message: string | null) => (message ? `errorCode=${code} · errorMessage="${message}"` : `errorCode=${code}`),
    endReasonField: (reason: string) => `endReason=${reason}`,
    statusField: (status: string) => `status=${status}`,
  },
  duration: (ms: number) => formatStepDuration(ms),
  clock: (time: string) => formatPreciseClock(time),
  locate: {
    viewing: 'Viewing the located reply',
    newMessages: 'New messages',
    backToLatest: 'Back to latest',
    loadNewer: 'Load newer messages',
    loadingNewer: 'Loading newer messages…',
    notFound: "Couldn't find that reply; showing the latest conversation.",
    target: 'Located reply',
  },
} satisfies Messages['timeline']
