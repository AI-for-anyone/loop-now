import { useState } from 'react'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import type { StepTone, TimelineStep } from './timeline-steps'

const DOT: Record<StepTone, string> = {
  neutral: 'bg-surface-canvas ring-[1.5px] ring-inset ring-ink-500/60',
  run: 'bg-ink-700',
  ok: 'bg-green/85',
  fail: 'bg-red',
  warn: 'bg-[var(--status-interrupt)]',
  hot: 'bg-pink-text',
}

// Jump to that question in the conversation (messages sent to you carry data-console-message-id); do nothing when the question isn't
// in the currently loaded conversation.
function QuestionLink({ id }: { id: string }) {
  const jump = () => {
    const target = document.querySelector<HTMLElement>(`[data-console-message-id="${id}"]`)
    target?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  }
  return (
    <button type="button" onClick={jump} className="self-start cursor-pointer text-[11.5px] font-medium text-riso-blue underline outline-none focus-visible:shadow-focus">
      {t.plugins.chat.ui.viewQuestion}
    </button>
  )
}

// Shown in code style on one line with an ellipsis under the step's name; hovering shows the full text, and clicking toggles the
// full wrapped view (leaving text selection intact).
function StepCode({ text }: { text: string }) {
  const [expanded, setExpanded] = useState(false)
  const toggle = () => {
    if (window.getSelection()?.toString()) return
    setExpanded((value) => !value)
  }
  return (
    <button
      type="button"
      aria-expanded={expanded}
      title={expanded ? undefined : text}
      onClick={toggle}
      className="mt-0.5 block min-w-0 max-w-full cursor-pointer self-start rounded-[4px] text-left text-ink-700 outline-none hover:text-riso-blue focus-visible:shadow-focus"
    >
      <code
        className={cn(
          'block min-w-0 max-w-full select-text font-mono text-[11.5px]',
          expanded ? 'whitespace-pre-wrap [overflow-wrap:anywhere]' : 'truncate',
        )}
      >
        {text}
      </code>
    </button>
  )
}

function Step({ step, last }: { step: TimelineStep; last: boolean }) {
  const code = step.command ?? step.target
  return (
    <li
      data-tone={step.tone}
      className="grid grid-cols-[minmax(0,78px)_14px_minmax(0,1fr)_max-content] gap-x-2"
    >
      <span className="pt-[2px] font-mono text-[10.5px] tabular-nums text-ink-500/85">
        {t.timeline.clock(step.at)}
      </span>
      <span aria-hidden="true" className="flex flex-col items-center">
        <span
          className={cn(
            'mt-[5px] shrink-0 rounded-full',
            step.sub ? 'size-1.5' : 'size-2',
            DOT[step.tone],
          )}
        />
        <span
          className={cn(
            'my-[3px] w-0 flex-1 border-l',
            last ? 'border-transparent' : 'border-hairline-strong',
          )}
        />
      </span>
      <span className={cn('flex min-w-0 flex-col pb-2', step.sub && 'pl-1.5')}>
        <span className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
          <span
            data-step-label
            className={cn(
              'font-medium text-ink',
              step.sub ? 'text-[11.5px]' : 'text-[12px]',
            )}
          >
            {step.label}
          </span>
          {step.field && <span className="font-mono text-[10.5px] text-ink-500/85">{step.field}</span>}
        </span>
        {code && <StepCode text={code} />}
        {step.detail && <span className="text-[11.5px] text-ink-500 [overflow-wrap:anywhere]">{step.detail}</span>}
        {step.lines.length > 0 && (
          <ul className="text-[11.5px] text-ink-500 [overflow-wrap:anywhere]">
            {step.lines.map((line, index) => (
              <li key={index}>{line}</li>
            ))}
          </ul>
        )}
        {step.questionId && <QuestionLink id={step.questionId} />}
        {step.output && (
          <pre className="mt-1 max-h-40 overflow-auto rounded-stamp border border-hairline bg-surface-canvas px-2 py-1 font-mono text-[11px] whitespace-pre-wrap [overflow-wrap:anywhere]">
            {step.output}
          </pre>
        )}
      </span>
      <span
        className={cn(
          'pt-[2px] text-right font-mono text-[11px] tabular-nums',
          step.tone === 'fail' ? 'font-medium text-red-text' : 'text-ink-500',
        )}
      >
        {step.duration}
      </span>
    </li>
  )
}

const USAGE_PARTS = [
  { key: 'input', bar: 'rounded-l-full bg-[var(--chart-blue)]' },
  { key: 'output', bar: 'bg-[var(--chart-pink)]' },
  { key: 'cacheRead', bar: 'bg-[var(--chart-ochre)]' },
  { key: 'cacheWrite', bar: 'rounded-r-full bg-[var(--chart-other)]' },
] as const

// reported: whether the runtime reported usage for this turn (Run.usageReported); when it didn't, its zeros mean "not known" and only that
// is said.
export function UsageBlock({ usage, reported = true }: { usage: Schemas['UsageTotals']; reported?: boolean }) {
  if (!reported) {
    return (
      <div className="flex flex-col gap-0.5 border-t border-hairline px-3 py-2">
        <span className="flex items-baseline gap-2">
          <span className="text-[11.5px] font-medium text-ink-700">{t.timeline.usage.title}</span>
          <span className="font-mono text-[10.5px] text-ink-500/85">{t.timeline.usage.field}</span>
          <span aria-hidden="true" className="ml-auto font-mono text-[12px] text-ink-500">—</span>
        </span>
        <span className="text-[11.5px] text-ink-500">{t.timeline.usage.unreported}</span>
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-1.5 border-t border-hairline px-3 py-2">
      <span className="flex items-baseline gap-2">
        <span className="text-[11.5px] font-medium text-ink-700">{t.timeline.usage.title}</span>
        <span className="font-mono text-[10.5px] text-ink-500/85">{t.timeline.usage.field}</span>
        <span className="ml-auto font-mono text-[12px] font-medium tabular-nums text-ink">
          {t.overview.tokens(usage.total)}
        </span>
      </span>
      {usage.total > 0 && (
        <span aria-hidden="true" className="flex h-1 gap-px overflow-hidden rounded-full opacity-75">
          {USAGE_PARTS.map((part) => (
            <span key={part.key} className={cn('h-full', part.bar)} style={{ width: `${(usage[part.key] / usage.total) * 100}%` }} />
          ))}
        </span>
      )}
      <span className="flex flex-wrap gap-x-3 gap-y-0.5 text-[11px] text-ink-500">
        {USAGE_PARTS.map((part) => (
          <span key={part.key}>
            {t.overview.usage.parts[part.key]}{' '}
            <b className="font-mono font-medium tabular-nums text-ink-700">
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
    <ol className="px-3 pt-2.5 pb-1">
      {steps.map((step, index) => (
        <Step key={step.key} step={step} last={index === steps.length - 1} />
      ))}
    </ol>
  )
}
