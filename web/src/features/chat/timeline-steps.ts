import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { endSummary } from './background-text'

type Entry = Schemas['TimelineEntry']
type Run = Schemas['Run']
type Runtime = Schemas['AgentRuntime']

// Node colors: neutral light blue, started-replying riso blue, success green, failure vermilion, in-progress yellow, interrupt
// fluorescent pink.
export type StepTone = 'neutral' | 'run' | 'ok' | 'fail' | 'warn' | 'hot'

export interface TimelineStep {
  key: string
  at: string
  label: string
  // The corresponding field name, shown in a monospaced font.
  field: string | null
  command: string | null
  detail: string | null
  // Additional notes listed one per line (each synced source, the tail of Pi's error output).
  lines: string[]
  // An output snippet shown verbatim (monospaced, keeping line breaks).
  output: string | null
  duration: string | null
  tone: StepTone
  // Sub-item: CLI install and config sync belong under “准备运行环境” (Preparing the environment), model calls come after
  // “开始回复” (Started replying); shown indented.
  sub: boolean
  // The question an extension's dialog request was turned into (an Inbox message in the conversation); when present, you can jump to it.
  questionId?: string | null
}

const ms = (from: string, to: string) => Date.parse(to) - Date.parse(from)
const fmt = (count: number) => t.overview.tokens(count)

function modelLabel(provider: string | null, model: string | null): string {
  return provider !== null && model !== null ? t.overview.usage.model(provider, model) : t.overview.usage.unknownModel
}

// Duration of a startup phase: until the next phase, the start of the reply, or the end.
const PHASE_BOUNDARY: ReadonlySet<Entry['type']> = new Set(['phase', 'running', 'ended'])

// Subscription usage limits reported by Claude Code: info is the object exactly as claude gave it; fields may be missing or not of the
// expected type, and missing ones are left out.
// Unknown limit types and statuses are shown as is; the reset time is a timestamp in seconds (milliseconds and date strings are
// tolerated too).
function rateLimitStep(info: Record<string, unknown>): { field: string | null; detail: string; tone: StepTone } {
  const copy = t.timeline.detail.rateLimit
  const text = (value: unknown) => (typeof value === 'string' && value.trim() ? value.trim() : null)
  const type = text(info.rateLimitType), status = text(info.status)
  const resets = resetTime(info.resetsAt)
  const detail = [type && (copy.type[type] ?? type), status && (copy.status[status] ?? status), resets && copy.resetsAt(resets)].filter(Boolean).join(' · ')
  const field = [type && `rateLimitType=${type}`, status && `status=${status}`].filter(Boolean).join(' · ')
  return { field: field || null, detail: detail || copy.empty, tone: status === 'rejected' ? 'fail' : status === 'allowed_warning' ? 'warn' : 'neutral' }
}

function resetTime(value: unknown): string | null {
  const ms = typeof value === 'number' && Number.isFinite(value) ? (value < 1e11 ? value * 1000 : value) : typeof value === 'string' ? Date.parse(value) : NaN
  return Number.isFinite(ms) && ms > 0 ? new Date(ms).toISOString() : null
}

// Sizes as a short figure: 9.8K, 16K.
const kilo = (value: number) => (value >= 1000 ? `${+(value / 1000).toFixed(1)}K` : String(value))

function endedLabel(entry: Extract<Entry, { type: 'ended' }>): { label: string; tone: StepTone } {
  if (entry.status === 'completed') return { label: t.timeline.step.ended.completed, tone: 'ok' }
  if (entry.status === 'failed') return { label: t.timeline.step.ended.failed, tone: 'fail' }
  if (entry.endReason === 'server_restart') return { label: t.timeline.step.ended.server_restart, tone: 'neutral' }
  if (entry.endReason === 'sleep_superseded' || entry.endReason === 'sleep_invalidated') return { label: t.chat.status[entry.endReason], tone: 'neutral' }
  return { label: t.timeline.step.ended.user_interrupt, tone: 'hot' }
}

// Turn timeline entries into steps for the UI. When there is no queued entry (sent while idle, or a reply from before this feature
// shipped), add a step at the run's queued time.
// bash shows the actual command from the record; no other arguments or tool output are shown.
// runtimeName gives a runtime's name (useRuntimeLookup().name). Startup phases and process entries carry their runtime; the server fills
// it in for records written before it was recorded.
export function buildSteps(run: Pick<Run, 'queuedAt' | 'whenBusy'> | null, entries: Entry[], runtimeName: (runtime: Runtime) => string): TimelineStep[] {
  const steps: TimelineStep[] = []
  const step = (entry: { type: string; at: string }, index: number, fields: Partial<TimelineStep> & { label: string }): TimelineStep => ({
    key: `${index}-${entry.type}`,
    at: entry.at,
    field: null,
    command: null,
    detail: null,
    lines: [],
    output: null,
    duration: null,
    tone: 'neutral',
    sub: false,
    ...fields,
  })

  if (run && !entries.some((entry) => entry.type === 'queued')) {
    steps.push(
      step({ type: 'queued', at: run.queuedAt }, -1, {
        label: t.timeline.step.queued,
        field: t.timeline.field.queued,
        detail: run.whenBusy ? t.timeline.detail.queued[run.whenBusy] : t.timeline.detail.idleDispatch,
      }),
    )
  }

  // The step of the most recent “Pi 启动” (Pi start) phase; the note about a newly started or reused process is merged into it.
  let launching: TimelineStep | null = null
  // Once the turn has ended, a tool still on started never reported its end (a record saved before the server closed such tools as
  // no_result): how it went is unknown, the same as no_result.
  const turnEnded = entries.some((entry) => entry.type === 'ended')
  entries.forEach((entry, index) => {
    switch (entry.type) {
      case 'sleep':
        steps.push(step(entry, index, {
          label: t.sleep.states[entry.sleep.state],
          field: t.sleep.wakeReference(entry.sleep.id),
          lines: [entry.sleep.wakeAt ? t.sleep.until(entry.sleep.wakeAt) : null, entry.sleep.endReason ? t.sleep.endReasons[entry.sleep.endReason] : null, entry.sleep.reason].filter((value): value is string => !!value),
        }))
        break
      case 'queued':
        steps.push(step(entry, index, { label: t.timeline.step.queued, field: t.timeline.field.queued, detail: t.timeline.detail.queued[entry.whenBusy] }))
        break
      case 'promoted':
        steps.push(
          step(entry, index, {
            label: t.timeline.step.promoted[entry.to],
            // When Inbox messages are converted one by one, this is recorded on the interrupt reminder that receives the message, with
            // the message ID.
            field: entry.messageId ? `${t.timeline.field.promoted(entry.from, entry.to)} · ${t.timeline.field.message(entry.messageId)}` : t.timeline.field.promoted(entry.from, entry.to),
            tone: entry.to === 'interrupt' ? 'hot' : 'neutral',
          }),
        )
        break
      case 'message_cancelled':
        steps.push(step(entry, index, { label: t.timeline.step.message_cancelled, field: t.timeline.field.message(entry.messageId) }))
        break
      case 'dispatched':
        steps.push(step(entry, index, { label: t.timeline.step.dispatched, field: t.timeline.field.dispatched }))
        break
      case 'phase': {
        const next = entries.slice(index + 1).find((other) => PHASE_BOUNDARY.has(other.type))
        const created = step(entry, index, {
          label: entry.phase === 'launching' ? t.timeline.step.launching(runtimeName(entry.runtime)) : (t.timeline.step[entry.phase] ?? t.common.unknown(entry.phase)),
          field: t.timeline.field.phase(entry.phase),
          duration: next ? t.timeline.duration(ms(entry.at, next.at)) : null,
        })
        steps.push(created)
        launching = entry.phase === 'launching' ? created : null
        break
      }
      case 'cli_install':
        steps.push(
          step(entry, index, {
            label: t.timeline.step.cli_install(entry.cli),
            detail: entry.result === 'succeeded' ? t.timeline.detail.installOk(entry.version) : t.timeline.detail.installFailed(entry.version, entry.error),
            duration: t.timeline.duration(entry.durationMs),
            tone: entry.result === 'succeeded' ? 'ok' : 'fail',
            sub: true,
          }),
        )
        break
      case 'workspace_sync': {
        const source = t.timeline.detail.syncSource
        steps.push(
          step(entry, index, {
            label: t.timeline.step.workspace_sync,
            detail: entry.result === 'succeeded' ? t.timeline.detail.syncOk(entry.sources.length) : t.timeline.detail.syncFailed(entry.error),
            lines: entry.sources.map((item) => {
              switch (item.outcome) {
                case 'cloned':
                  return source.cloned(item.path)
                case 'kept':
                  return source.kept(item.path)
                case 'files_synced':
                  return source.files_synced(item.path, item.filesWritten ?? 0, item.filesDeleted ?? 0)
                default:
                  return source.failed(item.path, item.error)
              }
            }),
            duration: t.timeline.duration(entry.durationMs),
            tone: entry.result === 'succeeded' ? 'ok' : 'fail',
            sub: true,
          }),
        )
        break
      }
      case 'pi_started':
      case 'pi_reused': {
        const detail =
          entry.type === 'pi_started'
            ? t.timeline.detail.processStarted(runtimeName(entry.runtime), entry.pid, t.timeline.detail.piStartReason[entry.reason] ?? entry.reason)
            : t.timeline.detail.processReused(runtimeName(entry.runtime), entry.pid)
        if (launching && launching.detail === null) {
          launching.detail = detail
        } else {
          steps.push(
            step(entry, index, {
              label: t.timeline.step.process_started(runtimeName(entry.runtime)),
              detail,
              duration: entry.type === 'pi_started' ? t.timeline.duration(entry.startupMs) : null,
            }),
          )
        }
        break
      }
      case 'pi_stopped':
        steps.push(
          step(entry, index, {
            label: t.timeline.step.process_stopped(runtimeName(entry.runtime)),
            detail: t.timeline.detail.processStopped(entry.pid, t.overview.run.exit[entry.reason] ?? entry.reason, t.overview.elapsed(entry.uptimeMs)),
            tone: 'warn',
          }),
        )
        break
      case 'pi_exited':
        steps.push(
          step(entry, index, {
            label: t.timeline.step.process_exited(runtimeName(entry.runtime)),
            detail: t.timeline.detail.processExited(entry.pid, entry.exit, t.overview.elapsed(entry.uptimeMs)),
            output: entry.stderrTail || null,
            tone: 'fail',
          }),
        )
        break
      case 'running':
        steps.push(step(entry, index, { label: t.timeline.step.running, field: t.timeline.field.running, tone: 'run' }))
        break
      case 'context_handoff':
        steps.push(
          step(entry, index, {
            label: t.timeline.step.context_handoff[entry.reason],
            // A lost session or a task's start begins a new session of the same runtime; a change of runtime goes from one to the other.
            detail: t.timeline.detail.handoff(
              entry.reason === 'runtime_changed'
                ? t.chat.handoff.route(runtimeName(entry.fromRuntime), runtimeName(entry.toRuntime))
                : t.chat.handoff.renewed(runtimeName(entry.toRuntime)),
              entry.includedMessages,
              entry.omittedMessages,
              entry.images,
              kilo(entry.bytes),
              kilo(entry.budgetBytes),
            ),
          }),
        )
        break
      case 'tool': {
        const command = entry.toolName === 'bash' && entry.command?.trim() ? entry.command : null
        const noResult = entry.state === 'no_result' || (entry.state === 'started' && turnEnded)
        const state = noResult ? t.timeline.detail.toolNoResult : entry.state === 'started' ? t.timeline.detail.toolRunning : null
        steps.push(
          step(entry, index, {
            label: t.timeline.step.tool(entry.toolName),
            field: t.timeline.field.tool(entry.state),
            command,
            detail: [entry.toolName === 'bash' && !command ? t.timeline.detail.commandUnavailable : null, state].filter(Boolean).join(' · ') || null,
            duration: entry.durationMs !== null ? t.timeline.duration(entry.durationMs) : null,
            tone: entry.state === 'succeeded' ? 'ok' : entry.state === 'failed' ? 'fail' : noResult ? 'neutral' : 'warn',
          }),
        )
        break
      }
      // A tool of this reply went on in the background: its label (the command, or what a sub-agent does). Its end may come long
      // after the reply: how it ended, why it was stopped, and the last part of its output.
      case 'background_task_started':
        steps.push(step(entry, index, { label: t.timeline.step.background_task_started, field: entry.taskId, command: entry.kind === 'command' ? entry.label : null, detail: entry.kind === 'command' ? null : `${t.background.kind[entry.kind]} · ${entry.label}`, tone: 'warn' }))
        break
      case 'background_task_ended': {
        const label = entries.find((other) => other.type === 'background_task_started' && other.taskId === entry.taskId)
        steps.push(
          step(entry, index, {
            label: t.timeline.step.background_task_ended,
            field: entry.taskId,
            command: label?.type === 'background_task_started' && label.kind === 'command' ? label.label : null,
            detail: endSummary({ status: entry.status, exitCode: entry.exitCode }),
            lines: entry.stopReason ? [t.background.card.stopReason[entry.stopReason]] : entry.status === 'unknown' ? [t.background.card.unknownHint] : [],
            output: entry.output || null,
            tone: entry.status === 'completed' ? 'ok' : entry.status === 'failed' ? 'fail' : 'neutral',
          }),
        )
        break
      }
      case 'background_wait_timeout':
        steps.push(step(entry, index, { label: t.timeline.step.background_wait_timeout(entry.limitMs) }))
        break
      case 'model_call':
        steps.push(
          step(entry, index, {
            label: t.timeline.step.model_call[entry.kind] ?? t.common.unknown(entry.kind),
            detail: t.timeline.detail.modelCall(
              modelLabel(entry.provider, entry.model),
              fmt(entry.input),
              fmt(entry.output),
              fmt(entry.cacheRead),
              fmt(entry.cacheWrite),
            ),
            duration: entry.durationMs !== null ? t.timeline.duration(entry.durationMs) : null,
            sub: true,
          }),
        )
        break
      case 'model_retry_started':
        steps.push(
          step(entry, index, {
            label: t.timeline.step.model_retry_started,
            detail: t.timeline.detail.retryStarted(entry.attempt, entry.maxAttempts, t.timeline.duration(entry.delayMs), entry.errorMessage),
            tone: 'warn',
          }),
        )
        break
      case 'model_retry_ended':
        steps.push(
          step(entry, index, {
            label: t.timeline.step.model_retry_ended,
            detail: t.timeline.detail.retryEnded(entry.success, entry.attempt, entry.finalError),
            tone: entry.success ? 'ok' : 'fail',
          }),
        )
        break
      case 'compaction': {
        const detail =
          entry.result === 'succeeded'
            ? t.timeline.detail.compaction.succeeded(
                entry.tokensBefore !== null ? fmt(entry.tokensBefore) : null,
                entry.estimatedTokensAfter !== null ? fmt(entry.estimatedTokensAfter) : null,
              )
            : entry.result === 'failed'
              ? t.timeline.detail.compaction.failed(entry.errorMessage)
              : t.timeline.detail.compaction.aborted
        steps.push(
          step(entry, index, {
            label: t.timeline.step.compaction,
            detail,
            duration: entry.durationMs !== null ? t.timeline.duration(entry.durationMs) : null,
            tone: entry.result === 'succeeded' ? 'ok' : entry.result === 'failed' ? 'fail' : 'warn',
          }),
        )
        break
      }
      case 'abort_requested':
        steps.push(
          entry.stop
            ? step(entry, index, { label: t.timeline.step.stop_requested, field: t.timeline.field.stop })
            : step(entry, index, { label: t.timeline.step.abort_requested, field: t.timeline.field.abort, tone: 'hot' }),
        )
        break
      // Requests from an Agent-side extension via ctx.ui: notifications and statuses are a faint step, warnings and errors are
      // colored; dialog-type requests state their outcome (answered, cancelled, expired),
      // and those turned into a question can jump to that question in the conversation.
      case 'extension_ui': {
        const ui = t.plugins.chat.ui
        const dialog = entry.method !== 'notify' && entry.method !== 'setStatus'
        steps.push(
          step(entry, index, {
            label: entry.method === 'notify' ? ui.notify : entry.method === 'setStatus' ? ui.setStatus : ui.dialog,
            field: entry.method === 'setStatus' ? entry.statusKey : entry.level ? ui.level[entry.level] : dialog ? entry.method : null,
            detail: entry.method === 'setStatus' && !entry.message ? ui.cleared : entry.message,
            lines: dialog && entry.answered ? [ui.answered[entry.answered]] : [],
            questionId: entry.questionId,
            tone: entry.level === 'error' ? 'fail' : entry.level === 'warning' ? 'warn' : 'neutral',
          }),
        )
        break
      }
      case 'session_missing':
        steps.push(
          step(entry, index, {
            label: t.timeline.step.session_missing,
            detail: entry.sessionFile ? t.timeline.detail.sessionFile(entry.sessionFile) : null,
            tone: 'fail',
          }),
        )
        break
      case 'model_rate_limit':
        steps.push(step(entry, index, { label: t.timeline.step.model_rate_limit, ...rateLimitStep(entry.info) }))
        break
      case 'ended': {
        const { label, tone } = endedLabel(entry)
        steps.push(step(entry, index, { label, field: t.timeline.field.ended(entry.status), tone }))
        break
      }
    }
  })
  return steps
}

// Total duration of a reply: from queued to ended; null while in progress.
export function runDurationMs(run: Pick<Run, 'queuedAt' | 'endedAt'>): number | null {
  return run.endedAt ? ms(run.queuedAt, run.endedAt) : null
}
