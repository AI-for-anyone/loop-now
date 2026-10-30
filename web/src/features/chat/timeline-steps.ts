import type { Schemas } from '@/api/client'
import { t } from '@/i18n'

type Entry = Schemas['TimelineEntry']
type Run = Schemas['Run']

// 节点颜色：中性浅蓝、开始回复孔版蓝、成功绿、失败朱红、进行中黄、打断荧光粉。
export type StepTone = 'neutral' | 'run' | 'ok' | 'fail' | 'warn' | 'hot'

export interface TimelineStep {
  key: string
  at: string
  label: string
  // 对应的字段名，等宽字体显示。
  field: string | null
  command: string | null
  detail: string | null
  // 逐条列出的补充说明（同步的各个来源、Pi 错误输出的末尾片段）。
  lines: string[]
  // 原样显示的输出片段（等宽、保留换行）。
  output: string | null
  duration: string | null
  tone: StepTone
  // 细项：CLI 安装、配置同步归在“准备运行环境”下，模型调用归在“开始回复”之后，缩进显示。
  sub: boolean
  // 扩展的对话请求转成的问题（对话里的一条 Inbox 消息）；有时可以跳到它。
  questionId?: string | null
}

const ms = (from: string, to: string) => Date.parse(to) - Date.parse(from)
const fmt = (count: number) => t.overview.tokens(count)

function modelLabel(provider: string | null, model: string | null): string {
  return provider !== null && model !== null ? t.overview.usage.model(provider, model) : t.overview.usage.unknownModel
}

// 启动阶段的用时：到下一个阶段、进入回复或结束为止。
const PHASE_BOUNDARY: ReadonlySet<Entry['type']> = new Set(['phase', 'running', 'ended'])

function endedLabel(entry: Extract<Entry, { type: 'ended' }>): { label: string; tone: StepTone } {
  if (entry.status === 'completed') return { label: t.timeline.step.ended.completed, tone: 'ok' }
  if (entry.status === 'failed') return { label: t.timeline.step.ended.failed, tone: 'fail' }
  if (entry.endReason === 'server_restart') return { label: t.timeline.step.ended.server_restart, tone: 'neutral' }
  if (entry.endReason === 'sleep_superseded' || entry.endReason === 'sleep_invalidated') return { label: t.chat.status[entry.endReason], tone: 'neutral' }
  return { label: t.timeline.step.ended.user_interrupt, tone: 'hot' }
}

// 把时间线条目转为界面上的步骤。没有入队条目时（空闲时发送，或本功能上线前的回复）按执行的入队时间补一步。
// bash 显示记录中的实际命令；不展示其他参数或工具输出。
export function buildSteps(run: Pick<Run, 'queuedAt' | 'whenBusy'> | null, entries: Entry[]): TimelineStep[] {
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

  // 最近一个“Pi 启动”阶段的步骤；新启动或复用进程的说明并入其中。
  let launching: TimelineStep | null = null
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
            // Inbox 消息逐条转换时记在接收它的打断提醒上，带消息 ID。
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
          label: t.timeline.step[entry.phase] ?? t.common.unknown(entry.phase),
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
            ? t.timeline.detail.piStarted(entry.pid, t.timeline.detail.piStartReason[entry.reason] ?? entry.reason)
            : t.timeline.detail.piReused(entry.pid)
        if (launching && launching.detail === null) {
          launching.detail = detail
        } else {
          steps.push(
            step(entry, index, {
              label: t.timeline.step[entry.type],
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
            label: t.timeline.step.pi_stopped,
            detail: t.timeline.detail.piStopped(entry.pid, t.overview.run.exit[entry.reason] ?? entry.reason, t.overview.elapsed(entry.uptimeMs)),
            tone: 'warn',
          }),
        )
        break
      case 'pi_exited':
        steps.push(
          step(entry, index, {
            label: t.timeline.step.pi_exited,
            detail: t.timeline.detail.piExited(entry.pid, entry.exit, t.overview.elapsed(entry.uptimeMs)),
            output: entry.stderrTail || null,
            tone: 'fail',
          }),
        )
        break
      case 'running':
        steps.push(step(entry, index, { label: t.timeline.step.running, field: t.timeline.field.running, tone: 'run' }))
        break
      case 'tool': {
        const command = entry.toolName === 'bash' && entry.command?.trim() ? entry.command : null
        steps.push(
          step(entry, index, {
            label: t.timeline.step.tool(entry.toolName),
            field: t.timeline.field.tool(entry.state),
            command,
            detail: [entry.toolName === 'bash' && !command ? t.timeline.detail.commandUnavailable : null, entry.state === 'started' ? t.timeline.detail.toolRunning : null].filter(Boolean).join(' · ') || null,
            duration: entry.durationMs !== null ? t.timeline.duration(entry.durationMs) : null,
            tone: entry.state === 'succeeded' ? 'ok' : entry.state === 'failed' ? 'fail' : 'warn',
          }),
        )
        break
      }
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
      // Agent 半边扩展经 ctx.ui 发来的请求：通知与状态是淡色的一步，警告、错误带颜色；对话类写明结局（已回答、已取消、已过期），
      // 转成了问题的可以跳到对话里的那个问题。
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
      case 'ended': {
        const { label, tone } = endedLabel(entry)
        steps.push(step(entry, index, { label, field: t.timeline.field.ended(entry.status), tone }))
        break
      }
    }
  })
  return steps
}

// 回复的总用时：从入队到结束；进行中时为 null。
export function runDurationMs(run: Pick<Run, 'queuedAt' | 'endedAt'>): number | null {
  return run.endedAt ? ms(run.queuedAt, run.endedAt) : null
}
