import { TerminalIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'
import { receiptClass, receiptPartClass } from '@/lib/receipt'
import { cn } from '@/lib/utils'
import { RunRecord } from './RunRecord'
import { runErrorText, useRunRuntimeName } from './run-status-text'
import { runStartedAt } from './time'

const text = t.chat.compactionNote

// The outcome of this compaction: on failure, the reason (stamp + error); when cancelled or interrupted, say so as it is;
// when completed normally, the tokens before and after compaction, or the reason nothing was compacted.
function Outcome({ run, compaction }: { run: Schemas['Run']; compaction: Schemas['RunCompaction'] }) {
  const runtimeName = useRunRuntimeName()
  if (run.status === 'failed') {
    const reason = runErrorText(run.errorCode, runtimeName(run))
    return (
      <p className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
        <Badge variant="stamp" tone="danger" className="[--stamp-tilt:-2deg]">
          {text.failed}
        </Badge>
        <span className="[overflow-wrap:anywhere]">{t.overview.anomalies.reason(reason, run.errorMessage)}</span>
      </p>
    )
  }
  if (run.status === 'interrupted') return <p>{text.interrupted}</p>
  if (run.status !== 'completed') return <p>{text.running}</p>
  if (compaction.outcome === 'skipped' && compaction.skipReason) return <p>{t.chat.commands.compactSkipped[compaction.skipReason]}</p>
  if (compaction.outcome === 'compacted' && compaction.tokensBefore != null && compaction.estimatedTokensAfter != null)
    return <p>{t.chat.commands.compact(compaction.tokensBefore, compaction.estimatedTokensAfter)}</p>
  // The runtime compacted but doesn't report the tokens (Codex).
  if (compaction.outcome === 'compacted') return <p>{t.chat.commands.compacted}</p>
  return <p>{t.chat.commands.noReply}</p>
}

// Located a manual compaction (/compact): it has no conversation messages and is not a turn in the conversation flow, so this note is
// shown where it started (highlighted with a light yellow fill, like a located reply).
// The header is a receipt like a command result's (command | Manual compaction | time); below it are the outcome and the explanation,
// and last the expanded run record (taken from the same timeline read).
export function CompactionNote({ agentId, run, className }: { agentId: string; run: Schemas['Run']; className?: string }) {
  const compaction = run.compaction
  if (!compaction) return null
  const command = compaction.instructions ? `/compact ${compaction.instructions}` : '/compact'
  const at = runStartedAt(run)
  return (
    <div data-run-id={run.id} data-located data-compaction-run className={cn('flex flex-col items-start gap-2.5 rounded-panel bg-tint-yellow p-4', className)}>
      <span className="sr-only">{text.target}</span>
      <div className={receiptClass}>
        <span className="inline-flex min-w-0 items-center gap-1.5 px-[11px]">
          <TerminalIcon aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2.2} />
          <code className="min-w-0 truncate font-mono font-bold">{command}</code>
        </span>
        <span className={receiptPartClass}>{text.title}</span>
        <time dateTime={at} className={cn(receiptPartClass, 'font-mono')}>
          {t.chat.time(at)}
        </time>
      </div>
      <div className="ml-1 flex flex-col gap-1.5 border-l-2 border-dashed border-ink/40 py-0.5 pl-3 text-[13.5px] leading-[1.6] text-ink-700">
        <Outcome run={run} compaction={compaction} />
        <p className="text-[12.5px] text-ink-500">{text.notATurn}</p>
      </div>
      <div className="w-full">
        <RunRecord agentId={agentId} run={run} defaultOpen />
      </div>
    </div>
  )
}
