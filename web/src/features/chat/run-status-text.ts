import type { Schemas } from '@/api/client'
import { t } from '@/i18n'

type Run = Schemas['Run']

// 排队中即作废、没有执行的轮次（撤回、消息全部转为打断、消息已被读取或取消）：对话流中隐藏，运行记录照常显示。
const VOIDED: ReadonlySet<Run['endReason']> = new Set(['user_cancelled', 'merged_into_interrupt', 'no_pending_messages'])
export const isVoidedRun = (run: Run) => VOIDED.has(run.endReason)

// 回复的状态文字。完成的回复不显示状态。
export function runStatusText(run: Run): string | null {
  switch (run.status) {
    case 'queued':
      return t.chat.queuedTag
    case 'starting':
      return run.phase ? (t.chat.status[run.phase] ?? t.common.unknown(run.phase)) : t.chat.status.starting
    case 'running':
    case 'aborting':
      return t.chat.status[run.status]
    case 'completed':
      return null
    case 'failed': {
      const reason = run.errorCode ? (t.chat.runError[run.errorCode] ?? t.common.unknown(run.errorCode)) : t.errors.unknown
      return t.chat.failure(reason, run.errorMessage)
    }
    case 'interrupted':
      return run.endReason ? (t.chat.status[run.endReason] ?? t.common.unknown(run.endReason)) : t.chat.status.user_interrupt
    default:
      return t.common.unknown(String(run.status))
  }
}

// 进行中执行的阶段文字，用于输入框上方的忙碌提示。
export function runPhaseText(run: Run): string {
  if (run.status === 'starting') return run.phase ? (t.chat.status[run.phase] ?? t.common.unknown(run.phase)) : t.chat.status.starting
  if (run.status === 'running' || run.status === 'aborting') return t.chat.status[run.status]
  return runStatusText(run) ?? ''
}
