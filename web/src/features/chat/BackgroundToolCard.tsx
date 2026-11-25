import { ChevronRightIcon, LoaderCircleIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { useNow } from '@/lib/use-now'
import { cn } from '@/lib/utils'
import { BackgroundKindIcon } from './BackgroundTasks'
import { elapsedClock } from './time'

type Task = Schemas['BackgroundTask']

// The card's frame by how the task stands: running is a dashed yellow ticket; an ended one is printed solid with an offset in its
// outcome's color (green finished, red failed, grey stopped or unknown).
const FRAME: Record<Task['status'], string> = {
  running: 'border-2 border-dashed border-ink bg-tint-yellow',
  completed: 'bg-paper-raised shadow-[0_0_0_2px_var(--ink),4px_4px_0_var(--green)]',
  failed: 'bg-paper-raised shadow-[0_0_0_2px_var(--ink),4px_4px_0_var(--red)]',
  stopped: 'bg-paper-raised shadow-[0_0_0_2px_var(--ink-500),4px_4px_0_rgb(24_38_94/0.18)]',
  unknown: 'bg-paper-raised shadow-[0_0_0_2px_var(--ink-500),4px_4px_0_rgb(24_38_94/0.18)]',
}

const PILL: Record<Exclude<Task['status'], 'running'>, string> = {
  completed: 'bg-tint-green text-green-text',
  failed: 'bg-tint-red text-red-text',
  stopped: 'border-[1.5px] border-dashed border-ink/40 text-ink-700',
  unknown: 'bg-ink/[0.07] text-ink-700',
}

function RunningPill({ task, stopping }: { task: Task; stopping: boolean }) {
  const now = useNow(1000)
  const clock = elapsedClock(now - Date.parse(task.startedAt))
  return (
    <span role="status" className="inline-flex min-h-[24px] shrink-0 items-center gap-1.5 rounded-full bg-riso-yellow px-2.5 text-[12.5px] leading-6 font-black whitespace-nowrap text-ink shadow-[inset_0_0_0_2px_var(--ink)]">
      <LoaderCircleIcon aria-hidden="true" className="riso-spin size-3.5 motion-reduce:animate-none" strokeWidth={3} />
      <span className="tabular-nums">{stopping ? t.background.card.stopping(clock) : t.background.card.runningFor(clock)}</span>
    </span>
  )
}

// What else is known about an ended task: its exit code, how long it took, and why it was stopped. An unknown end has none of these
// (its end time is only when Loop Now found it gone); it says why instead.
function endFacts(task: Task): string[] {
  const card = t.background.card
  if (task.status === 'unknown') return [card.unknownHint]
  const took = task.endedAt ? Date.parse(task.endedAt) - Date.parse(task.startedAt) : NaN
  return [
    task.exitCode !== null ? card.exitCode(task.exitCode) : null,
    Number.isFinite(took) && took >= 0 ? card.took(t.overview.elapsed(took)) : null,
    task.stopReason ? card.stopReason[task.stopReason] : null,
  ].filter((fact): fact is string => fact !== null)
}

// One tool of a reply that went on in the background (a command, a sub-agent): what it runs, and while it runs “在后台运行 · 1:23”
// (Running in the background · 1:23); once it has ended, how (status, exit code, how long it took) and the last part of its output.
// The person's reply has long ended by then; the card changes wherever that reply is shown.
export function BackgroundToolCard({ task, toolName }: { task: Task; toolName?: string }) {
  const ended = task.status !== 'running'
  const facts = ended ? endFacts(task) : []
  const card = t.background.card
  return (
    <div data-background-tool={task.id} data-status={task.status} className={cn('flex min-w-0 flex-col gap-2 rounded-ticket px-3.5 py-2.5', FRAME[task.status])}>
      <div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1.5">
        <span className="inline-flex min-w-0 flex-1 basis-[240px] items-center gap-2">
          <span aria-hidden="true" className="grid size-6 shrink-0 place-items-center rounded-[7px] bg-ink text-riso-yellow">
            <BackgroundKindIcon kind={task.kind} className="size-3.5" />
          </span>
          <span className="sr-only">{card.label}</span>
          {toolName && <span className="shrink-0 font-mono text-[12.5px] font-black text-ink">{toolName}</span>}
          <span title={task.label} className="min-w-0 truncate font-mono text-[12.5px] text-ink-700">
            {task.label}
          </span>
        </span>
        {ended ? (
          <span role="status" className={cn('inline-flex min-h-[22px] shrink-0 items-center rounded-full px-2.5 text-[12.5px] leading-[22px] font-black whitespace-nowrap', PILL[task.status as Exclude<Task['status'], 'running'>])}>
            {card.status[task.status]}
          </span>
        ) : (
          <RunningPill task={task} stopping={Boolean(task.stopRequestedAt)} />
        )}
      </div>
      {facts.length > 0 && (
        <p className="flex flex-wrap gap-x-1.5 pl-8 text-[12px] leading-5 font-bold text-ink-500">
          {facts.map((fact, index) => (
            <span key={index} className="inline-flex gap-x-1.5">
              {fact}
              {index < facts.length - 1 && <span aria-hidden="true">{t.chat.metaSeparator}</span>}
            </span>
          ))}
        </p>
      )}
      {ended && task.output && (
        <details className="group/output min-w-0 pl-8">
          <summary className="inline-flex cursor-pointer list-none items-center gap-1 text-[12.5px] font-black text-riso-blue [&::-webkit-details-marker]:hidden">
            <ChevronRightIcon aria-hidden="true" className="size-3.5 transition-transform group-open/output:rotate-90 motion-reduce:transition-none" strokeWidth={3} />
            {card.outputTail}
          </summary>
          <pre className="riso-halftone mt-2 max-h-72 min-w-0 overflow-auto rounded-input bg-riso-blue px-3.5 py-2.5 font-mono text-[12.5px] leading-[1.55] whitespace-pre-wrap text-white shadow-print-pink [overflow-wrap:anywhere]">
            {task.output}
          </pre>
        </details>
      )}
    </div>
  )
}

// The background tools of a reply, in the order they started.
export function BackgroundToolCards({ tasks, toolNames }: { tasks: Task[]; toolNames?: ReadonlyMap<string, string> }) {
  if (tasks.length === 0) return null
  const ordered = [...tasks].sort((a, b) => Date.parse(a.startedAt) - Date.parse(b.startedAt))
  return (
    <ul aria-label={t.background.title} className="flex min-w-0 flex-col gap-2.5">
      {ordered.map((task) => (
        <li key={task.id} className="min-w-0">
          <BackgroundToolCard task={task} toolName={task.toolCallId ? toolNames?.get(task.toolCallId) : undefined} />
        </li>
      ))}
    </ul>
  )
}
