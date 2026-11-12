import { TerminalIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'
import { receiptClass, receiptPartClass } from '@/lib/receipt'
import { cn } from '@/lib/utils'
import { RunRecord } from './RunRecord'
import { runStartedAt } from './time'

const text = t.chat.compactionNote

// 这次压缩的结果：失败写原因（印章 + 错误），取消或中断照实写，正常完成写压缩前后的 token 或没有压缩的原因。
function Outcome({ run, compaction }: { run: Schemas['Run']; compaction: Schemas['RunCompaction'] }) {
  if (run.status === 'failed') {
    const reason = run.errorCode ? (t.chat.runError[run.errorCode] ?? t.common.unknown(run.errorCode)) : t.errors.unknown
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
  return <p>{t.chat.commands.noReply}</p>
}

// 定位到一次手动压缩（/compact）：它没有对话消息，不是对话流里的一轮，在它开始的位置显示这张说明（与定位到的回复一样用浅黄底突出）。
// 抬头是和命令结果一样的小票（命令｜手动压缩｜时间），下面是结果和说明，最后是展开的运行记录（取自同一次读到的时间线）。
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
