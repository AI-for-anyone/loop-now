import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import type { StepTone, TimelineStep } from './timeline-steps'

const DOT: Record<StepTone, string> = {
  neutral: 'bg-surface-canvas ring-[1.5px] ring-inset ring-ink-500/60 [[data-skin=plate]_&]:bg-tint-blue [[data-skin=plate]_&]:ring-0 [[data-skin=plate]_&]:shadow-[inset_0_0_0_2px_var(--riso-blue)]',
  run: 'bg-ink-700 [[data-skin=plate]_&]:bg-riso-blue [[data-skin=plate]_&]:shadow-[0_0_0_2px_var(--ink)]',
  ok: 'bg-green/85 [[data-skin=plate]_&]:bg-green [[data-skin=plate]_&]:shadow-[0_0_0_2px_var(--ink)]',
  fail: 'bg-red [[data-skin=plate]_&]:shadow-[0_0_0_2px_var(--ink)]',
  warn: 'bg-[var(--status-interrupt)] [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:shadow-[0_0_0_2px_var(--ink)]',
  hot: 'bg-pink-text [[data-skin=plate]_&]:bg-riso-pink [[data-skin=plate]_&]:shadow-[0_0_0_2px_var(--ink)]',
}

// Jump to that question in the conversation (messages sent to you carry data-console-message-id); do nothing when the question isn't
// in the currently loaded conversation.
function QuestionLink({ id }: { id: string }) {
  const jump = () => {
    const target = document.querySelector<HTMLElement>(`[data-console-message-id="${id}"]`)
    target?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  }
  return (
    <button type="button" onClick={jump} className="self-start cursor-pointer text-[11.5px] font-medium text-riso-blue underline outline-none focus-visible:shadow-focus [[data-skin=plate]_&]:text-[12.5px] [[data-skin=plate]_&]:font-bold">
      {t.plugins.chat.ui.viewQuestion}
    </button>
  )
}

function Step({ step, last }: { step: TimelineStep; last: boolean }) {
  return (
    <li
      data-tone={step.tone}
      className="grid grid-cols-[minmax(0,78px)_14px_minmax(0,1fr)_max-content] gap-x-2 [[data-skin=plate]_&]:grid-cols-[minmax(0,92px)_20px_minmax(0,1fr)_max-content] [[data-skin=plate]_&]:gap-x-2.5"
    >
      <span className="pt-[2px] font-mono text-[10.5px] tabular-nums text-ink-500/85 [[data-skin=plate]_&]:pt-px [[data-skin=plate]_&]:text-[11.5px] [[data-skin=plate]_&]:text-ink-500">
        {t.timeline.clock(step.at)}
      </span>
      <span aria-hidden="true" className="flex flex-col items-center">
        <span
          className={cn(
            'mt-[5px] shrink-0 rounded-full [[data-skin=plate]_&]:mt-[3px]',
            step.sub ? 'size-1.5 [[data-skin=plate]_&]:size-2' : 'size-2 [[data-skin=plate]_&]:size-3',
            DOT[step.tone],
          )}
        />
        <span
          className={cn(
            'my-[3px] w-0 flex-1 border-l [[data-skin=plate]_&]:border-l-2 [[data-skin=plate]_&]:border-dashed',
            last ? 'border-transparent' : 'border-hairline-strong [[data-skin=plate]_&]:border-ink/25',
          )}
        />
      </span>
      <span className={cn('flex min-w-0 flex-col pb-2 [[data-skin=plate]_&]:pb-3', step.sub && 'pl-1.5 [[data-skin=plate]_&]:pl-2')}>
        <span className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
          <span
            data-step-label
            className={cn(
              'font-medium text-ink [[data-skin=plate]_&]:font-black',
              step.sub ? 'text-[11.5px] [[data-skin=plate]_&]:text-[13px]' : 'text-[12px] [[data-skin=plate]_&]:text-[13.5px]',
            )}
          >
            {step.label}
          </span>
          {step.command && <code className="min-w-0 max-w-full whitespace-pre-wrap font-mono text-[11px] text-ink-700 [overflow-wrap:anywhere] [[data-skin=plate]_&]:text-[12px] [[data-skin=plate]_&]:text-ink">{step.command}</code>}
          {step.field && <span className="font-mono text-[10.5px] text-ink-500/85 [[data-skin=plate]_&]:text-[11px] [[data-skin=plate]_&]:text-ink-500">{step.field}</span>}
        </span>
        {step.detail && <span className="text-[11.5px] text-ink-500 [overflow-wrap:anywhere] [[data-skin=plate]_&]:text-[12.5px] [[data-skin=plate]_&]:text-ink-700">{step.detail}</span>}
        {step.lines.length > 0 && (
          <ul className="text-[11.5px] text-ink-500 [overflow-wrap:anywhere] [[data-skin=plate]_&]:text-[12.5px] [[data-skin=plate]_&]:text-ink-700">
            {step.lines.map((line, index) => (
              <li key={index}>{line}</li>
            ))}
          </ul>
        )}
        {step.questionId && <QuestionLink id={step.questionId} />}
        {step.output && (
          <pre className="mt-1 max-h-40 overflow-auto rounded-stamp border border-hairline bg-surface-canvas px-2 py-1 font-mono text-[11px] whitespace-pre-wrap [overflow-wrap:anywhere] [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue [[data-skin=plate]_&]:text-[11.5px]">
            {step.output}
          </pre>
        )}
      </span>
      <span
        className={cn(
          'pt-[2px] text-right font-mono text-[11px] tabular-nums [[data-skin=plate]_&]:pt-px [[data-skin=plate]_&]:text-[12px] [[data-skin=plate]_&]:font-bold',
          step.tone === 'fail' ? 'font-medium text-red-text' : 'text-ink-500 [[data-skin=plate]_&]:text-ink',
        )}
      >
        {step.duration}
      </span>
    </li>
  )
}

const USAGE_PARTS = [
  { key: 'input', bar: 'rounded-l-full bg-[var(--chart-blue)] [[data-skin=plate]_&]:rounded-l-[4px] [[data-skin=plate]_&]:bg-riso-blue' },
  { key: 'output', bar: 'bg-[var(--chart-pink)] [[data-skin=plate]_&]:bg-riso-pink' },
  { key: 'cacheRead', bar: 'bg-[var(--chart-ochre)] [[data-skin=plate]_&]:bg-tint-blue [[data-skin=plate]_&]:shadow-[inset_0_0_0_2px_var(--riso-blue)]' },
  { key: 'cacheWrite', bar: 'rounded-r-full bg-[var(--chart-other)] [[data-skin=plate]_&]:rounded-r-[4px] [[data-skin=plate]_&]:bg-tint-pink-2 [[data-skin=plate]_&]:shadow-[inset_0_0_0_2px_var(--pink-text)]' },
] as const

// reported: whether the runtime reported usage for this turn (Run.usageReported); when it didn't, its zeros mean "not known" and only that
// is said.
export function UsageBlock({ usage, reported = true }: { usage: Schemas['UsageTotals']; reported?: boolean }) {
  if (!reported) {
    return (
      <div className="flex flex-col gap-0.5 border-t border-hairline px-3 py-2 [[data-skin=plate]_&]:gap-1 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/15 [[data-skin=plate]_&]:px-4 [[data-skin=plate]_&]:py-3">
        <span className="flex items-baseline gap-2 [[data-skin=plate]_&]:gap-2.5">
          <span className="text-[11.5px] font-medium text-ink-700 [[data-skin=plate]_&]:text-[13.5px] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-ink">{t.timeline.usage.title}</span>
          <span className="font-mono text-[10.5px] text-ink-500/85 [[data-skin=plate]_&]:text-[11px] [[data-skin=plate]_&]:text-ink-500">{t.timeline.usage.field}</span>
          <span aria-hidden="true" className="ml-auto font-mono text-[12px] text-ink-500 [[data-skin=plate]_&]:font-display-latin [[data-skin=plate]_&]:text-[22px] [[data-skin=plate]_&]:font-extrabold">—</span>
        </span>
        <span className="text-[11.5px] text-ink-500 [[data-skin=plate]_&]:text-[12.5px] [[data-skin=plate]_&]:text-ink-700">{t.timeline.usage.unreported}</span>
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-1.5 border-t border-hairline px-3 py-2 [[data-skin=plate]_&]:gap-2 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/15 [[data-skin=plate]_&]:px-4 [[data-skin=plate]_&]:py-3">
      <span className="flex items-baseline gap-2 [[data-skin=plate]_&]:gap-2.5">
        <span className="text-[11.5px] font-medium text-ink-700 [[data-skin=plate]_&]:text-[13.5px] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-ink">{t.timeline.usage.title}</span>
        <span className="font-mono text-[10.5px] text-ink-500/85 [[data-skin=plate]_&]:text-[11px] [[data-skin=plate]_&]:text-ink-500">{t.timeline.usage.field}</span>
        <span className="ml-auto font-mono text-[12px] font-medium tabular-nums text-ink [[data-skin=plate]_&]:font-display-latin [[data-skin=plate]_&]:text-[22px] [[data-skin=plate]_&]:font-extrabold">
          {t.overview.tokens(usage.total)}
        </span>
      </span>
      {usage.total > 0 && (
        <span aria-hidden="true" className="flex h-1 gap-px overflow-hidden rounded-full opacity-75 [[data-skin=plate]_&]:h-3 [[data-skin=plate]_&]:gap-0.5 [[data-skin=plate]_&]:overflow-visible [[data-skin=plate]_&]:rounded-none [[data-skin=plate]_&]:opacity-100">
          {USAGE_PARTS.map((part) => (
            <span key={part.key} className={cn('h-full', part.bar)} style={{ width: `${(usage[part.key] / usage.total) * 100}%` }} />
          ))}
        </span>
      )}
      <span className="flex flex-wrap gap-x-3 gap-y-0.5 text-[11px] text-ink-500 [[data-skin=plate]_&]:gap-x-4 [[data-skin=plate]_&]:text-[12.5px] [[data-skin=plate]_&]:text-ink-700">
        {USAGE_PARTS.map((part) => (
          <span key={part.key}>
            {t.overview.usage.parts[part.key]}{' '}
            <b className="font-mono font-medium tabular-nums text-ink-700 [[data-skin=plate]_&]:font-sans [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-ink">
              {t.overview.tokens(usage[part.key])}
            </b>
          </span>
        ))}
        <span>{t.timeline.usage.calls(usage.calls)}</span>
      </span>
    </div>
  )
}

export function TimelineSteps({ steps }: { steps: TimelineStep[] }) {
  return (
    <ol className="px-3 pt-2.5 pb-1 [[data-skin=plate]_&]:px-4 [[data-skin=plate]_&]:pt-3.5 [[data-skin=plate]_&]:pb-0.5">
      {steps.map((step, index) => (
        <Step key={step.key} step={step} last={index === steps.length - 1} />
      ))}
    </ol>
  )
}
