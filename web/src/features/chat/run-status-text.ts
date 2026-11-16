import type { Schemas } from '@/api/client'
import { t } from '@/i18n'

type Run = Schemas['Run']

// Turns voided while still queued that never ran (withdrawn, all messages turned into interrupts, messages already read or cancelled):
// hidden in the conversation flow, shown as usual in the run record.
const VOIDED: ReadonlySet<Run['endReason']> = new Set(['user_cancelled', 'merged_into_interrupt', 'no_pending_messages'])
export const isVoidedRun = (run: Run) => VOIDED.has(run.endReason)

// Status text of a reply. A completed reply shows no status.
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

// Phase text of a run in progress, used for the busy hint above the input box.
export function runPhaseText(run: Run): string {
  if (run.status === 'starting') return run.phase ? (t.chat.status[run.phase] ?? t.common.unknown(run.phase)) : t.chat.status.starting
  if (run.status === 'running' || run.status === 'aborting') return t.chat.status[run.status]
  return runStatusText(run) ?? ''
}
