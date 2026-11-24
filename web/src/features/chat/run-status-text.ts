import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { useRuntimeLookup, type AgentRuntime } from '@/lib/runtime'

type Run = Schemas['Run']

// Why a run failed, in words. The runtime_* failures name the runtime that failed (runtimeName; "the runtime" when it isn't known).
export function runErrorText(code: string | null, runtimeName: string | null = null): string {
  if (!code) return t.errors.unknown
  const named = (t.chat.runtimeError as Record<string, ((name: string | null) => string) | undefined>)[code]
  if (named) return named(runtimeName)
  return (t.chat.runError as Record<string, string | undefined>)[code] ?? t.common.unknown(code)
}

// The name of the runtime a run's failure is about: the one the turn was handed to (Run.selection), otherwise the given one (the Agent's
// now, for a turn that failed before it began), otherwise none.
export function useRunRuntimeName(fallback?: AgentRuntime | null) {
  const lookup = useRuntimeLookup()
  return (run: Pick<Run, 'selection'>) => {
    const id = run.selection?.runtime ?? fallback
    return id ? lookup.name(id) : null
  }
}

// Turns voided while still queued that never ran (withdrawn, all messages turned into interrupts, messages already read or cancelled):
// hidden in the conversation flow, shown as usual in the run record.
const VOIDED: ReadonlySet<Run['endReason']> = new Set(['user_cancelled', 'merged_into_interrupt', 'no_pending_messages'])
export const isVoidedRun = (run: Run) => VOIDED.has(run.endReason)

// Status text of a reply. A completed reply shows no status.
export function runStatusText(run: Run, runtimeName: string | null = null): string | null {
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
      return t.chat.failure(runErrorText(run.errorCode, runtimeName), run.errorMessage)
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
