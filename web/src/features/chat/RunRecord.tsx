import { useId, useState } from 'react'
import { ChevronDownIcon, ChevronUpIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { useRunTimeline } from '@/api/queries'
import { RequestId } from '@/components/RequestError'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'
import { describeError, requestIdOf } from '@/lib/errors'
import { useNow } from '@/lib/use-now'
import { cn } from '@/lib/utils'
import { isVoidedRun, runStatusText } from './run-status-text'
import { buildSteps, runDurationMs } from './timeline-steps'
import { TimelineSteps, UsageBlock } from './TimelineBody'

type Run = Schemas['Run']

const ENDED: ReadonlySet<Run['status']> = new Set(['completed', 'failed', 'interrupted'])

// 结局：印章、字段与原因。原因文字沿用对话页的 chat.failure 与 chat.status.*。
function Result({ run }: { run: Run }) {
  if (!ENDED.has(run.status)) return null
  const restart = run.status === 'interrupted' && run.endReason === 'server_restart'
  const stopped = run.status === 'interrupted' && run.endReason === 'user_stopped'
  const voided = isVoidedRun(run)
  const style =
    run.status === 'completed'
      ? { box: 'bg-tint-green', label: t.timeline.result.completed, tone: 'success' as const, stamp: 'text-green-text' }
      : run.status === 'failed'
        ? { box: 'bg-tint-red', label: t.timeline.result.failed, tone: 'danger' as const, stamp: '' }
        : restart || stopped || voided
          ? { box: 'bg-tint-blue', label: restart ? t.timeline.result.server_restart : stopped ? t.timeline.result.stopped : t.timeline.result.voided, tone: 'restart' as const, stamp: '' }
          : { box: 'bg-tint-pink-2', label: t.timeline.result.interrupted, tone: 'interrupted' as const, stamp: '' }
  const field =
    run.status === 'failed'
      ? t.timeline.result.errorField(run.errorCode ?? 'unknown', run.errorMessage)
      : run.endReason
        ? t.timeline.result.endReasonField(run.endReason)
        : t.timeline.result.statusField(run.status)
  return (
    <div className={cn('flex flex-wrap items-center gap-x-3 gap-y-1.5 px-4 py-3', style.box)}>
      <Badge variant="stamp" tone={style.tone} className={cn('[--stamp-tilt:-2deg]', style.stamp)}>
        {style.label}
      </Badge>
      <span className="font-mono text-[11.5px] [overflow-wrap:anywhere]">{field}</span>
      <span className="text-[13.5px] [overflow-wrap:anywhere]">{runStatusText(run) ?? t.timeline.detail.completed}</span>
    </div>
  )
}

function Timeline({ timeline }: { timeline: Schemas['RunTimeline'] }) {
  const { run, entries, usage } = timeline
  const steps = buildSteps(run, entries)
  const duration = runDurationMs(run)
  const meta = [
    run.model ? t.timeline.meta.model(run.model.provider, run.model.modelId) : t.timeline.meta.noModel,
    t.timeline.meta.config(run.configVersion),
    t.timeline.meta.whenBusy[run.whenBusy ?? 'idle'],
  ].join(` ${t.chat.metaSeparator} `)
  return (
    <>
      <div className="flex flex-wrap items-center gap-x-3.5 gap-y-1 bg-tint-blue px-4 py-2.5 text-[12.5px]">
        <span className="font-black">{t.timeline.title}</span>
        <span className="font-mono text-[11.5px] [overflow-wrap:anywhere]">{run.id}</span>
        <span className="text-ink-700">{meta}</span>
        {duration !== null && <span className="ml-auto font-black">{t.timeline.total(t.timeline.duration(duration))}</span>}
      </div>
      <TimelineSteps steps={steps} />
      <UsageBlock usage={usage} />
      <Result run={run} />
    </>
  )
}

interface RunRecordProps {
  agentId: string
  run: Run
  // 从概览定位到这条回复时自动展开；其余默认收起。
  defaultOpen?: boolean
}

// 回复下方的运行记录：收起时是一张小票式的按钮，写着总用时与 token 数（进行中只写已用时间）；展开时请求时间线。
export function RunRecord({ agentId, run, defaultOpen = false }: RunRecordProps) {
  const [open, setOpen] = useState(defaultOpen)
  const panelId = useId()
  const ended = ENDED.has(run.status)
  const now = useNow(1000, !ended)
  const timeline = useRunTimeline(agentId, run.id, open, ended)
  const duration = runDurationMs(run)
  const done = ended && duration !== null
  const elapsed = t.overview.elapsed(now - Date.parse(run.queuedAt))
  const label = done ? t.timeline.toggle(t.timeline.duration(duration), t.overview.tokens(run.usage.total)) : t.timeline.toggleRunning(elapsed)
  // 小票上名称之后的各段：结束后是耗时与 token 数，进行中只有已用时间。
  const parts = done ? [t.timeline.duration(duration), t.timeline.toggleParts.tokens(t.overview.tokens(run.usage.total))] : [t.timeline.toggleParts.running(elapsed)]

  return (
    <div className="flex flex-col gap-2">
      {/* 触发条是一张分段的小票：名称｜耗时｜token，段与段之间一道虚线。读屏读的仍是连在一起的一句（sr-only），各段只给眼睛看。 */}
      <button
        type="button"
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        onClick={() => setOpen((value) => !value)}
        className="mt-0.5 inline-flex max-w-full items-stretch self-start rounded-[6px] bg-tint-blue text-[12px] leading-7 text-ink outline-none hover:bg-[#d6ddf4] focus-visible:shadow-focus"
      >
        <span className="sr-only">{label}</span>
        <span aria-hidden="true" data-receipt-part className="px-[11px] font-black whitespace-nowrap">
          {t.timeline.title}
        </span>
        {parts.map((part, index) => (
          <span key={index} aria-hidden="true" data-receipt-part className="border-l-[1.5px] border-dashed border-ink/40 px-[11px] font-mono font-medium whitespace-nowrap">
            {part}
          </span>
        ))}
        {/* 换图标而不旋转：按钮内的元素也不做变换。 */}
        <span className="inline-flex items-center pr-[9px]">
          {open ? (
            <ChevronUpIcon aria-hidden="true" strokeWidth={2.8} className="size-3" />
          ) : (
            <ChevronDownIcon aria-hidden="true" strokeWidth={2.8} className="size-3" />
          )}
        </span>
      </button>
      {open && (
        <div id={panelId} className="flex flex-col overflow-hidden rounded-[16px] bg-white shadow-[3px_3px_0_rgb(24_38_94/.12)]">
          {timeline.isError ? (
            <div role="alert" className="flex flex-col gap-1.5 bg-tint-red px-4 py-3 text-[13.5px] text-red-text">
              <span>{describeError(timeline.error)}</span>
              {requestIdOf(timeline.error) && (
                <span className="text-ink">
                  <RequestId id={requestIdOf(timeline.error)!} />
                </span>
              )}
            </div>
          ) : timeline.data ? (
            <Timeline timeline={timeline.data} />
          ) : (
            <p role="status" className="px-4 py-3 text-[13px] font-bold text-ink-500">
              {t.timeline.loading}
            </p>
          )}
        </div>
      )}
    </div>
  )
}
