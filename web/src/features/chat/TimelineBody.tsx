import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { pluginChatText } from '@/i18n/plugins'
import { cn } from '@/lib/utils'
import type { StepTone, TimelineStep } from './timeline-steps'

const DOT: Record<StepTone, string> = {
  neutral: 'bg-tint-blue shadow-[inset_0_0_0_2px_var(--riso-blue)]',
  run: 'bg-riso-blue shadow-[0_0_0_2px_var(--ink)]',
  ok: 'bg-green shadow-[0_0_0_2px_var(--ink)]',
  fail: 'bg-red shadow-[0_0_0_2px_var(--ink)]',
  warn: 'bg-riso-yellow shadow-[0_0_0_2px_var(--ink)]',
  hot: 'bg-riso-pink shadow-[0_0_0_2px_var(--ink)]',
}

// 跳到对话里的那个问题（发给你的消息带 data-console-message-id）；问题不在当前加载的对话里时不动。
function QuestionLink({ id }: { id: string }) {
  const jump = () => {
    const target = document.querySelector<HTMLElement>(`[data-console-message-id="${id}"]`)
    target?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  }
  return (
    <button type="button" onClick={jump} className="self-start cursor-pointer text-[12.5px] font-bold text-riso-blue underline outline-none focus-visible:shadow-focus">
      {pluginChatText.ui.viewQuestion}
    </button>
  )
}

function Step({ step, last }: { step: TimelineStep; last: boolean }) {
  return (
    <li data-tone={step.tone} className="grid grid-cols-[minmax(0,92px)_20px_minmax(0,1fr)_max-content] gap-x-2.5">
      <span className="pt-px font-mono text-[11.5px] text-ink-500">{t.timeline.clock(step.at)}</span>
      <span aria-hidden="true" className="flex flex-col items-center">
        <span className={cn('mt-[3px] shrink-0 rounded-full', step.sub ? 'size-2' : 'size-3', DOT[step.tone])} />
        <span className={cn('my-[3px] w-0 flex-1 border-l-2 border-dashed', last ? 'border-transparent' : 'border-ink/25')} />
      </span>
      <span className={cn('flex min-w-0 flex-col pb-3', step.sub && 'pl-2')}>
        <span className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
          <span className={cn('font-black', step.sub ? 'text-[13px]' : 'text-[13.5px]')}>{step.label}</span>
          {step.command && <code className="min-w-0 max-w-full whitespace-pre-wrap font-mono text-[12px] [overflow-wrap:anywhere]">{step.command}</code>}
          {step.field && <span className="font-mono text-[11px] text-ink-500">{step.field}</span>}
        </span>
        {step.detail && <span className="text-[12.5px] [overflow-wrap:anywhere] text-ink-700">{step.detail}</span>}
        {step.lines.length > 0 && (
          <ul className="text-[12.5px] [overflow-wrap:anywhere] text-ink-700">
            {step.lines.map((line, index) => (
              <li key={index}>{line}</li>
            ))}
          </ul>
        )}
        {step.questionId && <QuestionLink id={step.questionId} />}
        {step.output && (
          <pre className="mt-1 max-h-40 overflow-auto rounded-stamp bg-tint-blue px-2 py-1 font-mono text-[11.5px] whitespace-pre-wrap [overflow-wrap:anywhere]">
            {step.output}
          </pre>
        )}
      </span>
      <span className={cn('pt-px text-right font-mono text-[12px] font-bold', step.tone === 'fail' ? 'text-red-text' : 'text-ink')}>
        {step.duration}
      </span>
    </li>
  )
}

const USAGE_PARTS = [
  { key: 'input', bar: 'rounded-l-[4px] bg-riso-blue' },
  { key: 'output', bar: 'bg-riso-pink' },
  { key: 'cacheRead', bar: 'bg-tint-blue shadow-[inset_0_0_0_2px_var(--riso-blue)]' },
  { key: 'cacheWrite', bar: 'rounded-r-[4px] bg-tint-pink-2 shadow-[inset_0_0_0_2px_var(--pink-text)]' },
] as const

export function UsageBlock({ usage }: { usage: Schemas['UsageTotals'] }) {
  return (
    <div className="flex flex-col gap-2 border-t-2 border-dashed border-ink/15 px-4 py-3">
      <span className="flex items-baseline gap-2.5">
        <span className="text-[13.5px] font-black">{t.timeline.usage.title}</span>
        <span className="font-mono text-[11px] text-ink-500">{t.timeline.usage.field}</span>
        <span className="ml-auto font-display-latin text-[22px] font-extrabold">{t.overview.tokens(usage.total)}</span>
      </span>
      {usage.total > 0 && (
        <span aria-hidden="true" className="flex h-3 gap-0.5">
          {USAGE_PARTS.map((part) => (
            <span key={part.key} className={cn('h-full', part.bar)} style={{ width: `${(usage[part.key] / usage.total) * 100}%` }} />
          ))}
        </span>
      )}
      <span className="flex flex-wrap gap-x-4 gap-y-0.5 text-[12.5px] text-ink-700">
        {USAGE_PARTS.map((part) => (
          <span key={part.key}>
            {t.overview.usage.parts[part.key]} <b className="text-ink">{t.overview.tokens(usage[part.key])}</b>
          </span>
        ))}
        <span>{t.timeline.usage.calls(usage.calls)}</span>
      </span>
    </div>
  )
}


export function TimelineSteps({ steps }: { steps: TimelineStep[] }) { return <ol className="px-4 pt-3.5 pb-0.5">{steps.map((step, index) => <Step key={step.key} step={step} last={index === steps.length - 1} />)}</ol> }
