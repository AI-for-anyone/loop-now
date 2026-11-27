import { useId, useState } from 'react'
import { ChevronDownIcon, ChevronUpIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { useRunTimeline } from '@/api/queries'
import { RequestId } from '@/components/RequestError'
import { t } from '@/i18n'
import { describeError, requestIdOf } from '@/lib/errors'
import { useRuntimeLookup } from '@/lib/runtime'
import { useNow } from '@/lib/use-now'
import { cn } from '@/lib/utils'
import { useSkin } from '@/theme/store'
import { isVoidedRun, runStatusText, useRunRuntimeName } from './run-status-text'
import { buildSteps, runDurationMs } from './timeline-steps'
import { TimelineSteps, UsageBlock } from './TimelineBody'

type Run = Schemas['Run']

const ENDED: ReadonlySet<Run['status']> = new Set(['completed', 'failed', 'interrupted'])

// Outcome: quiet status footer driven by semantic theme tokens.
function Result({ run }: { run: Run }) {
  const runtimeName = useRunRuntimeName()
  if (!ENDED.has(run.status)) return null
  const restart = run.status === 'interrupted' && run.endReason === 'server_restart'
  const stopped = run.status === 'interrupted' && run.endReason === 'user_stopped'
  const voided = isVoidedRun(run)
  const style =
    run.status === 'completed'
      ? { dot: 'bg-green', labelColor: 'text-green-text', label: t.timeline.result.completed }
      : run.status === 'failed'
        ? { dot: 'bg-red', labelColor: 'text-red-text', label: t.timeline.result.failed }
        : restart || stopped || voided
          ? {
              dot: 'bg-ink-500',
              labelColor: 'text-ink-700',
              label: restart ? t.timeline.result.server_restart : stopped ? t.timeline.result.stopped : t.timeline.result.voided,
            }
          : { dot: 'bg-pink-text', labelColor: 'text-pink-text', label: t.timeline.result.interrupted }
  const field =
    run.status === 'failed'
      ? t.timeline.result.errorField(run.errorCode ?? 'unknown', run.errorMessage)
      : run.endReason
        ? t.timeline.result.endReasonField(run.endReason)
        : t.timeline.result.statusField(run.status)
  return (
    <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1 border-t border-hairline px-3 py-2 text-[11.5px]">
      <span className={cn('inline-flex items-center gap-1.5 font-medium', style.labelColor)}>
        <span aria-hidden="true" className={cn('size-1.5 shrink-0 rounded-full', style.dot)} />
        {style.label}
      </span>
      <span className="font-mono text-[10.5px] text-ink-500/85 [overflow-wrap:anywhere]">{field}</span>
      <span className="text-[11.5px] text-ink-500 [overflow-wrap:anywhere]">
        {runStatusText(run, runtimeName(run)) ?? t.timeline.detail.completed}
      </span>
    </div>
  )
}

function Timeline({ timeline }: { timeline: Schemas['RunTimeline'] }) {
  const { run, entries, usage } = timeline
  const runtimes = useRuntimeLookup()
  const steps = buildSteps(run, entries, runtimes.name)
  const duration = runDurationMs(run)
  const meta = [
    run.model ? t.timeline.meta.model(run.model.provider, run.model.modelId) : t.timeline.meta.noModel,
    t.timeline.meta.config(run.configVersion),
    t.timeline.meta.whenBusy[run.whenBusy ?? 'idle'],
  ].join(` ${t.chat.metaSeparator} `)
  return (
    <>
      <div className="flex flex-wrap items-center gap-x-2.5 gap-y-0.5 border-b border-hairline px-3 py-2 text-[11.5px]">
        <span className="font-medium text-ink-700">{t.timeline.title}</span>
        <span className="font-mono text-[10.5px] text-ink-500/85 [overflow-wrap:anywhere]">{run.id}</span>
        <span className="text-[11px] text-ink-500">{meta}</span>
        {duration !== null && (
          <span className="ml-auto font-mono text-[11.5px] font-medium tabular-nums text-ink-700">
            {t.timeline.total(t.timeline.duration(duration))}
          </span>
        )}
      </div>
      <TimelineSteps steps={steps} />
      <UsageBlock usage={usage} reported={run.usageReported} />
      <Result run={run} />
    </>
  )
}

interface RunRecordProps {
  agentId: string
  run: Run
  // Expanded automatically when this reply was located from the overview; collapsed by default otherwise.
  defaultOpen?: boolean
}

// The run record below a reply: when collapsed it renders as a compact single-line metadata row; expanding it requests the timeline.
export function RunRecord({ agentId, run, defaultOpen = false }: RunRecordProps) {
  const skin = useSkin()
  const plate = skin === 'plate'
  const [open, setOpen] = useState(defaultOpen)
  const panelId = useId()
  const ended = ENDED.has(run.status)
  const now = useNow(1000, !ended)
  const timeline = useRunTimeline(agentId, run.id, open, ended)
  const duration = runDurationMs(run)
  const done = ended && duration !== null
  const elapsed = t.overview.elapsed(now - Date.parse(run.queuedAt))
  const label = !done ? t.timeline.toggleRunning(elapsed) : run.usageReported ? t.timeline.toggle(t.timeline.duration(duration), t.overview.tokens(run.usage.total)) : t.timeline.toggleNoUsage(t.timeline.duration(duration))
  // The parts after the name: once ended, the duration and token count (compact in mono skin, exact in Plate skin, or that the runtime
  // reported none); while in progress, only the elapsed time.
  const tokenPart = plate
    ? t.timeline.toggleParts.tokens(t.overview.tokens(run.usage.total))
    : t.timeline.toggleParts.compactTokens(run.usage.total)
  const parts = done ? [t.timeline.duration(duration), run.usageReported ? tokenPart : t.timeline.toggleParts.noUsage] : [t.timeline.toggleParts.running(elapsed)]

  return (
    <div className="flex flex-col gap-1.5">
      {/* Screen readers read one joined sentence (sr-only); visual segments stay inline and compact. */}
      <button
        type="button"
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        title={!plate && done && run.usageReported ? label : undefined}
        onClick={() => setOpen((value) => !value)}
        className={cn(
          'inline-flex max-w-full flex-wrap items-center gap-x-1.5 self-start rounded-stamp py-0.5 leading-5 transition-colors outline-none hover:text-ink-700 focus-visible:shadow-focus',
          plate ? 'mt-0.5 text-[11.5px] text-ink-500' : 'text-[12.5px] text-ink-500/80',
        )}
      >
        <span className="sr-only">{label}</span>
        <span aria-hidden="true" data-receipt-part className="font-normal whitespace-nowrap">
          {t.timeline.title}
        </span>
        {parts.map((part, index) => (
          <span key={index} aria-hidden="true" className="inline-flex items-center gap-x-1.5">
            <span className="text-ink-500/45 select-none">{t.chat.metaSeparator}</span>
            <span
              data-receipt-part
              aria-hidden="true"
              className={cn('font-normal tabular-nums whitespace-nowrap', plate && 'font-mono')}
            >
              {part}
            </span>
          </span>
        ))}
        {/* Swap the icon instead of rotating it: elements inside buttons get no transforms either. */}
        <span className="inline-flex items-center text-ink-500/70">
          {open ? (
            <ChevronUpIcon aria-hidden="true" strokeWidth={1.8} className="size-3" />
          ) : (
            <ChevronDownIcon aria-hidden="true" strokeWidth={1.8} className="size-3" />
          )}
        </span>
      </button>
      {open && (
        <div
          id={panelId}
          className="flex flex-col overflow-hidden rounded-card border border-hairline bg-surface-subtle/50"
        >
          {timeline.isError ? (
            <div role="alert" className="flex flex-col gap-1 bg-tint-red px-3 py-2 text-[12px] text-red-text">
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
            <p role="status" className="px-3 py-2 text-[11.5px] text-ink-500">
              {t.timeline.loading}
            </p>
          )}
        </div>
      )}
    </div>
  )
}
