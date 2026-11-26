import { LoaderCircleIcon } from 'lucide-react'
import { Popover as PopoverPrimitive } from 'radix-ui'
import type { Schemas } from '@/api/client'
import { badgeVariants } from '@/components/ui/badge'
import { t } from '@/i18n'
import { useNow } from '@/lib/use-now'
import { cn } from '@/lib/utils'
import { BackgroundKindIcon } from './BackgroundTasks'
import { elapsedClock } from './time'

type Task = Schemas['BackgroundTask']

// The chip's colors by how the task stands, as the other tool chips: running blue, finished green, failed red, stopped or unknown grey.
const TONE: Record<Task['status'], string> = {
  running: 'bg-tint-blue text-ink',
  completed: 'bg-tint-green text-green-text',
  failed: 'bg-tint-red text-red-text',
  stopped: 'bg-ink/[0.07] text-ink-700',
  unknown: 'bg-ink/[0.07] text-ink-700',
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

const statusText = (task: Task) => (task.status === 'running' && task.stopRequestedAt ? t.background.stopping : t.background.card.status[task.status])

// While it runs, the details say for how long; this is the only clock (the chip has none), and it ticks only while they are open.
function RunningFor({ task }: { task: Task }) {
  const now = useNow(1000)
  return <span className="tabular-nums">{t.background.elapsedLabel(elapsedClock(now - Date.parse(task.startedAt)))}</span>
}

// The details of a background tool, opened from its chip: what it runs, how long it has run or how it ended, and its last output.
function Details({ task, name }: { task: Task; name: string }) {
  const facts = task.status === 'running' ? [] : endFacts(task)
  return (
    <>
      <div className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b-2 border-ink bg-tint-yellow px-4 py-2.5">
        <span aria-hidden="true" className="grid size-6 shrink-0 place-items-center rounded-[7px] bg-ink text-riso-yellow">
          <BackgroundKindIcon kind={task.kind} className="size-3.5" />
        </span>
        <span className="font-mono text-[13px] font-black">{name}</span>
        <span className={cn(badgeVariants({ variant: 'chip' }), TONE[task.status])}>{statusText(task)}</span>
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-2.5 overflow-y-auto px-4 py-3">
        <p className="font-mono text-[13px] leading-[1.5] font-bold [overflow-wrap:anywhere] whitespace-pre-wrap text-ink">{task.label}</p>
        {(task.status === 'running' || facts.length > 0) && (
          <p className="flex flex-wrap gap-x-1.5 text-[12.5px] leading-5 font-bold text-ink-500">
            {task.status === 'running' ? (
              <RunningFor task={task} />
            ) : (
              facts.map((fact, index) => (
                <span key={index} className="inline-flex gap-x-1.5">
                  {fact}
                  {index < facts.length - 1 && <span aria-hidden="true">{t.chat.metaSeparator}</span>}
                </span>
              ))
            )}
          </p>
        )}
        {task.status !== 'running' && task.output && (
          <div className="flex min-w-0 flex-col gap-1.5">
            <span className="text-[12.5px] font-black text-ink">{t.background.card.outputTail}</span>
            <pre className="riso-halftone max-h-64 min-w-0 overflow-auto rounded-input bg-riso-blue px-3.5 py-2.5 font-mono text-[12.5px] leading-[1.55] whitespace-pre-wrap text-white [overflow-wrap:anywhere]">
              {task.output}
            </pre>
          </div>
        )}
      </div>
    </>
  )
}

// One tool of a reply that went on in the background (a command, a sub-agent), as a chip among the reply's other tools: “Bash 后台运行中”
// (Bash Running in the background) with a spinner while it runs, then how it ended. The details are behind a click. The person's reply has
// long ended by then; the chip changes wherever that reply is shown.
function BackgroundToolChip({ task, toolName }: { task: Task; toolName?: string }) {
  const name = toolName ?? t.background.kind[task.kind]
  const status = statusText(task)
  return (
    <PopoverPrimitive.Root>
      <PopoverPrimitive.Trigger
        type="button"
        data-background-tool={task.id}
        data-status={task.status}
        aria-label={t.background.card.chipLabel(name, status, task.label)}
        title={task.label}
        className={cn(badgeVariants({ variant: 'chip' }), TONE[task.status], 'cursor-pointer outline-none hover:shadow-[inset_0_0_0_1.5px_currentColor] focus-visible:shadow-focus aria-expanded:shadow-[inset_0_0_0_2px_var(--ink)]')}
      >
        {task.status === 'running' && (
          <>
            <LoaderCircleIcon aria-hidden="true" data-slot="tool-spinner" className="riso-spin motion-reduce:hidden motion-reduce:animate-none" />
            <span aria-hidden="true" className="hidden motion-reduce:inline">{t.chat.toolStaticMark}</span>
          </>
        )}
        {/* The chip lines up with the reply's other tool chips by its text, not by the spinner in front of it. */}
        <span className="self-baseline font-mono text-ink">{name}</span>
        <span>{status}</span>
      </PopoverPrimitive.Trigger>
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Content
          side="bottom"
          align="start"
          sideOffset={8}
          collisionPadding={12}
          aria-label={t.background.card.label}
          className="z-50 flex max-h-[min(460px,calc(100dvh-120px))] w-[min(440px,calc(100vw-24px))] flex-col overflow-hidden rounded-card bg-paper-raised text-ink shadow-[0_0_0_2px_var(--ink),6px_6px_0_2px_var(--riso-yellow)] outline-none"
        >
          <Details task={task} name={name} />
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  )
}

// The background tools of a reply, in the order they started, as items of the reply's tool list.
export function BackgroundToolChips({ tasks, toolNames }: { tasks: Task[]; toolNames?: ReadonlyMap<string, string> }) {
  const ordered = [...tasks].sort((a, b) => Date.parse(a.startedAt) - Date.parse(b.startedAt))
  return ordered.map((task) => (
    <li key={task.id}>
      <BackgroundToolChip task={task} toolName={task.toolCallId ? toolNames?.get(task.toolCallId) : undefined} />
    </li>
  ))
}
