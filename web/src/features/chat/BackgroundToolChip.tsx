import { ArrowLeftIcon, LoaderCircleIcon } from 'lucide-react'
import { useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { Popover as PopoverPrimitive } from 'radix-ui'
import { ApiError, type Schemas } from '@/api/client'
import { badgeVariants } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { describeError } from '@/lib/errors'
import { useNow } from '@/lib/use-now'
import { cn } from '@/lib/utils'
import { useSkin } from '@/theme/store'
import { BackgroundKindIcon } from './BackgroundTasks'
import { backgroundStatusText, groupBackgroundTasks, shownBackgroundState, type BackgroundToolGroup } from './background-text'
import { useQueueActions } from './queue-actions'
import { elapsedClock } from './time'
import { useToolListLeading } from './tool-list-context'

type Task = Schemas['BackgroundTask']

// The chip's colors by how the task stands, as the other tool chips: running blue, finished green, failed red, stopped or unknown grey.
const TONE: Record<Task['status'], string> = {
  running: 'bg-tint-blue text-ink',
  completed: 'bg-tint-green text-green-text',
  failed: 'bg-tint-red text-red-text',
  stopped: 'bg-surface-subtle text-ink-700',
  unknown: 'bg-surface-subtle text-ink-700',
}

const alreadyEnded = (error: unknown) => error instanceof ApiError && error.code === 'background_task_ended'

type StopTaskFn = (taskId: string, actionId: string) => Promise<unknown>

interface TaskStopControl {
  stop: (taskId: string) => Promise<void>
  stopping: (task: Task) => boolean
  retry: (taskId: string) => boolean
  error: (taskId: string) => unknown
}

// Per-task stop state shared across a reply's background tool popovers so stopping state and unknown-outcome retries
// survive closing and reopening a popover or navigating between the group list and a task's details.
function useTaskStop(onStop?: StopTaskFn): TaskStopControl | undefined {
  const actions = useQueueActions()
  const [requested, setRequested] = useState<ReadonlySet<string>>(() => new Set())
  const [errors, setErrors] = useState<Record<string, unknown>>({})
  if (!onStop) return undefined
  return {
    stop: async (taskId: string) => {
      const result = await actions.run(taskId, 'stop', (actionId) => onStop(taskId, actionId))
      if (!result) return
      const failed = !result.ok && !alreadyEnded(result.error)
      setErrors((old) => ({ ...old, [taskId]: failed ? result.error : undefined }))
      if (!failed) setRequested((old) => new Set([...old, taskId]))
    },
    stopping: (task: Task) => actions.pending.has(task.id) || requested.has(task.id) || Boolean(task.stopRequestedAt),
    retry: (taskId: string) => actions.isUnknown(taskId, 'stop'),
    error: (taskId: string) => errors[taskId],
  }
}

function StopTaskButton({ task, control, className }: { task: Task; control: TaskStopControl; className?: string }) {
  const stopping = control.stopping(task)
  return (
    <Button
      type="button"
      size="xs"
      variant="danger-outline"
      className={cn(stopping && 'border-dashed bg-tint-red disabled:opacity-100', className)}
      disabled={stopping}
      aria-label={stopping ? t.background.stopping : t.background.stopLabel(task.label)}
      onClick={() => void control.stop(task.id)}
    >
      {stopping && <LoaderCircleIcon aria-hidden="true" className="riso-spin size-3 motion-reduce:animate-none" strokeWidth={3} />}
      {stopping ? t.background.stopping : control.retry(task.id) ? t.background.retryStop : t.background.stop}
    </Button>
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

const statusText = (task: Task) => backgroundStatusText(shownBackgroundState(task))

// While it runs, the details say for how long; this is the only clock (the tool line has none), and it ticks only while open.
function RunningFor({ task }: { task: Task }) {
  const now = useNow(1000)
  return <span className="tabular-nums">{t.background.elapsedLabel(elapsedClock(now - Date.parse(task.startedAt)))}</span>
}

// Header line of a background task's details: its kind icon, tool name (or kind), and status pill.
export function BackgroundTaskHead({ task, name }: { task: Task; name: string }) {
  return (
    <>
      <span aria-hidden="true" className="grid size-6 shrink-0 place-items-center rounded-[7px] border border-hairline bg-surface-elevated text-ink">
        <BackgroundKindIcon kind={task.kind} className="size-3.5" />
      </span>
      <span className="font-mono text-[13px] font-semibold">{name}</span>
      <span className={cn(badgeVariants({ variant: 'chip' }), TONE[task.status])}>{statusText(task)}</span>
    </>
  )
}

// Body of a background task's details: what it runs, how long it has run or how it ended, and its last output.
export function BackgroundTaskBody({ task }: { task: Task }) {
  const facts = task.status === 'running' ? [] : endFacts(task)
  return (
    <>
      <p className="font-mono text-[13px] leading-[1.5] font-medium [overflow-wrap:anywhere] whitespace-pre-wrap text-ink">{task.label}</p>
      {(task.status === 'running' || facts.length > 0) && (
        <p className="flex flex-wrap gap-x-1.5 text-[12.5px] leading-5 font-medium text-ink-500">
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
          <span className="text-[12.5px] font-semibold text-ink">{t.background.card.outputTail}</span>
          <pre className="riso-halftone max-h-64 min-w-0 overflow-auto rounded-input border border-code-border bg-code-bg px-3.5 py-2.5 font-mono text-[12.5px] leading-[1.55] whitespace-pre-wrap text-code-fg [overflow-wrap:anywhere]">
            {task.output}
          </pre>
        </div>
      )}
    </>
  )
}

// Popover frame shared by background tool entries and the "+N" overflow chip in follow-up headings.
export function BackgroundTaskPopover({ label, children }: { label: string; children: ReactNode }) {
  return (
    <PopoverPrimitive.Portal>
      <PopoverPrimitive.Content
        side="bottom"
        align="start"
        sideOffset={8}
        collisionPadding={12}
        aria-label={label}
        className="z-50 flex max-h-[min(460px,calc(100dvh-120px))] w-[min(440px,calc(100vw-24px))] flex-col overflow-hidden rounded-card border border-hairline-strong bg-surface-elevated text-ink shadow-floating outline-none"
      >
        {children}
      </PopoverPrimitive.Content>
    </PopoverPrimitive.Portal>
  )
}

// The details of a single background tool: what it runs, how long it has run or how it ended, its last output,
// and an optional Back button (when drilled in from a multi-task group) and Stop button (while running).
function Details({
  task,
  name,
  stopControl,
  onBack,
}: {
  task: Task
  name: string
  stopControl?: TaskStopControl
  onBack?: { label: string; onClick: () => void }
}) {
  const error = stopControl?.error(task.id)
  return (
    <>
      <div className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b border-hairline bg-surface-subtle px-4 py-2.5">
        {onBack && (
          <button
            type="button"
            autoFocus
            aria-label={onBack.label}
            onClick={onBack.onClick}
            className="inline-flex h-6 shrink-0 cursor-pointer items-center gap-1 rounded-stamp px-1.5 -ml-1.5 text-[12px] font-medium text-ink-700 transition-colors outline-none hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus"
          >
            <ArrowLeftIcon aria-hidden="true" className="size-3.5" />
            <span>{t.background.card.back}</span>
          </button>
        )}
        <BackgroundTaskHead task={task} name={name} />
        {task.status === 'running' && stopControl && <StopTaskButton task={task} control={stopControl} className="ml-auto" />}
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-2.5 overflow-y-auto px-4 py-3">
        <BackgroundTaskBody task={task} />
        {error !== undefined && (
          <p role="alert" className="text-[12.5px] font-semibold text-red-text">
            {describeError(error)}
          </p>
        )}
      </div>
    </>
  )
}

// List view inside a multi-task group popover: each task's label, status, elapsed time, and Details / Stop actions.
function GroupTaskList({
  group,
  label,
  stopControl,
  focusTaskId,
  onSelect,
}: {
  group: BackgroundToolGroup
  label: string
  stopControl?: TaskStopControl
  focusTaskId: string | null
  onSelect: (taskId: string) => void
}) {
  const now = useNow(1000, group.status === 'running')
  const status = backgroundStatusText(group.state)
  const detailsRefs = useRef<Map<string, HTMLButtonElement>>(new Map())

  // Restore keyboard focus to the task's Details button when returning from its detail view instead of dropping to <body>.
  useLayoutEffect(() => {
    if (!focusTaskId) return
    const target = detailsRefs.current.get(focusTaskId) ?? detailsRefs.current.values().next().value
    target?.focus()
  }, [focusTaskId])

  return (
    <>
      <div className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b border-hairline bg-surface-subtle px-4 py-2.5">
        <span aria-hidden="true" className="grid size-6 shrink-0 place-items-center rounded-[7px] border border-hairline bg-surface-elevated text-ink">
          <BackgroundKindIcon kind={group.tasks[0].kind} className="size-3.5" />
        </span>
        <span className="inline-flex items-baseline gap-1">
          <span className="font-mono text-[13px] font-semibold">{group.name}</span>
          <span className="text-[12.5px] font-medium tabular-nums text-ink-500">×{group.tasks.length}</span>
        </span>
        <span className={cn(badgeVariants({ variant: 'chip' }), TONE[group.status])}>{status}</span>
      </div>
      <ul aria-label={label} className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        {group.tasks.map((task) => {
          const facts = task.status === 'running' ? [] : endFacts(task)
          const clock = task.status === 'running' ? elapsedClock(now - Date.parse(task.startedAt)) : null
          const error = stopControl?.error(task.id)
          return (
            <li key={task.id} data-background-group-task={task.id} className="flex flex-col gap-1.5 border-b border-hairline px-4 py-3 last:border-b-0">
              <p title={task.label} className="line-clamp-2 font-mono text-[13px] leading-[1.45] font-medium [overflow-wrap:anywhere] text-ink">
                {task.label}
              </p>
              <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1.5">
                <p className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-[12px] leading-5 font-medium text-ink-500">
                  <span>{statusText(task)}</span>
                  {clock !== null ? (
                    <>
                      <span aria-hidden="true">{t.chat.metaSeparator}</span>
                      <span className="tabular-nums">{t.background.elapsedLabel(clock)}</span>
                    </>
                  ) : (
                    facts.map((fact, index) => (
                      <span key={index} className="inline-flex items-center gap-x-1.5">
                        <span aria-hidden="true">{t.chat.metaSeparator}</span>
                        <span>{fact}</span>
                      </span>
                    ))
                  )}
                </p>
                <div className="flex shrink-0 items-center gap-1.5">
                  <Button
                    ref={(node) => {
                      if (node) detailsRefs.current.set(task.id, node)
                      return () => {
                        detailsRefs.current.delete(task.id)
                      }
                    }}
                    type="button"
                    size="xs"
                    variant="secondary"
                    aria-label={t.background.card.detailsLabel(task.label)}
                    onClick={() => onSelect(task.id)}
                  >
                    {t.background.card.details}
                  </Button>
                  {task.status === 'running' && stopControl && <StopTaskButton task={task} control={stopControl} />}
                </div>
              </div>
              {error !== undefined && (
                <p role="alert" className="text-[12.5px] font-semibold text-red-text">
                  {describeError(error)}
                </p>
              )}
            </li>
          )
        })}
      </ul>
    </>
  )
}

// One tool of a reply that went on in the background, rendered as a pill chip (used in Plate skin and follow-up headings).
// Name and status share one baseline inside the chip while remaining vertically centered next to the spinner.
export function BackgroundToolChip({ task, toolName }: { task: Task; toolName?: string }) {
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
        className={cn(badgeVariants({ variant: 'chip' }), TONE[task.status], 'cursor-pointer outline-none hover:border-hairline-strong focus-visible:shadow-focus aria-expanded:border-ink')}
      >
        {task.status === 'running' && (
          <>
            <LoaderCircleIcon aria-hidden="true" data-slot="tool-spinner" className="riso-spin motion-reduce:hidden motion-reduce:animate-none" />
            <span aria-hidden="true" data-slot="tool-static-mark" className="hidden motion-reduce:inline">{t.chat.toolStaticMark}</span>
          </>
        )}
        <span data-tool-text="" className="inline-flex items-baseline gap-1">
          <span className="font-mono text-ink">{name}</span>
          <span>{status}</span>
        </span>
      </PopoverPrimitive.Trigger>
      <BackgroundTaskPopover label={t.background.card.label}>
        <Details task={task} name={name} />
      </BackgroundTaskPopover>
    </PopoverPrimitive.Root>
  )
}

// One grouped background tool entry on the quiet tool line (default mono skin): one clickable button per (name, state) group.
function BackgroundToolGroupItem({
  group,
  separator,
  stopControl,
}: {
  group: BackgroundToolGroup
  separator: boolean
  stopControl?: TaskStopControl
}) {
  const [open, setOpen] = useState(false)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [returnFocusId, setReturnFocusId] = useState<string | null>(null)
  const single = group.tasks.length === 1 ? group.tasks[0] : null
  const status = backgroundStatusText(group.state)
  const ariaLabel = single
    ? t.background.card.chipLabel(group.name, status, single.label)
    : t.background.card.groupLabel(group.name, status, group.tasks.length)
  const popoverLabel = single ? t.background.card.label : ariaLabel
  const selected = !single && selectedId ? (group.tasks.find((task) => task.id === selectedId) ?? null) : null

  const handleOpenChange = (next: boolean) => {
    setOpen(next)
    if (!next) {
      setSelectedId(null)
      setReturnFocusId(null)
    }
  }

  const handleSelect = (taskId: string) => {
    setReturnFocusId(null)
    setSelectedId(taskId)
  }

  const handleBack = () => {
    if (selected) setReturnFocusId(selected.id)
    setSelectedId(null)
  }

  return (
    <li
      data-tool-group=""
      data-background-tool-group=""
      data-status={group.status}
      className="inline-flex items-center"
    >
      {separator && (
        <span aria-hidden="true" data-tool-separator="" className="mx-1.5 text-ink-500/45 select-none">
          {t.chat.metaSeparator}
        </span>
      )}
      <PopoverPrimitive.Root open={open} onOpenChange={handleOpenChange}>
        <PopoverPrimitive.Trigger
          type="button"
          data-background-tool={single ? single.id : undefined}
          data-background-tool-count={group.tasks.length}
          data-status={group.status}
          aria-label={ariaLabel}
          title={single ? single.label : ariaLabel}
          className={cn(
            'group/bg-tool -mx-1 inline-flex cursor-pointer items-center gap-1 rounded-stamp px-1 text-left transition-colors outline-none focus-visible:shadow-focus',
            group.status === 'failed'
              ? 'text-red-text hover:bg-tint-red/70 aria-expanded:bg-tint-red/70'
              : 'text-ink-500 hover:bg-surface-hover hover:text-ink aria-expanded:bg-surface-hover aria-expanded:text-ink',
          )}
        >
          {group.status === 'running' && (
            <>
              <LoaderCircleIcon
                aria-hidden="true"
                data-slot="tool-spinner"
                className="size-3 shrink-0 riso-spin motion-reduce:hidden motion-reduce:animate-none"
              />
              <span aria-hidden="true" data-slot="tool-static-mark" className="hidden motion-reduce:inline">
                {t.chat.toolStaticMark}
              </span>
            </>
          )}
          <span data-tool-text="" className="inline-flex items-baseline gap-1">
            <span
              className={cn(
                'font-mono text-[12px] font-medium',
                group.status === 'running' || group.status === 'completed'
                  ? 'text-ink-700 group-hover/bg-tool:text-ink group-aria-expanded/bg-tool:text-ink'
                  : undefined,
              )}
            >
              {group.name}
            </span>
            {group.tasks.length > 1 && (
              <span className={cn('tabular-nums', group.status === 'failed' ? 'text-red-text/75' : 'text-ink-500/70')}>
                ×{group.tasks.length}
              </span>
            )}
            <span>{status}</span>
          </span>
        </PopoverPrimitive.Trigger>
        <BackgroundTaskPopover label={popoverLabel}>
          {single ? (
            <Details task={single} name={group.name} stopControl={stopControl} />
          ) : selected ? (
            <Details
              task={selected}
              name={group.name}
              stopControl={stopControl}
              onBack={{
                label: t.background.card.backLabel(group.name, group.tasks.length),
                onClick: handleBack,
              }}
            />
          ) : (
            <GroupTaskList
              group={group}
              label={ariaLabel}
              stopControl={stopControl}
              focusTaskId={returnFocusId ?? selectedId}
              onSelect={handleSelect}
            />
          )}
        </BackgroundTaskPopover>
      </PopoverPrimitive.Root>
    </li>
  )
}

// The background tools of a reply, in the order they started, as items of the reply's tool list.
// In the default mono skin, tasks with the same name and state are grouped onto the quiet tool line;
// in Plate skin, each task stays an individual pill.
export function BackgroundToolChips({
  tasks,
  toolNames,
  onStop,
}: {
  tasks: Task[]
  toolNames?: ReadonlyMap<string, string>
  onStop?: StopTaskFn
}) {
  const skin = useSkin()
  const hasLeadingTools = useToolListLeading()
  const stopControl = useTaskStop(onStop)

  if (skin === 'plate') {
    const ordered = [...tasks].sort((a, b) => Date.parse(a.startedAt) - Date.parse(b.startedAt))
    return ordered.map((task) => (
      <li key={task.id}>
        <BackgroundToolChip task={task} toolName={task.toolCallId ? toolNames?.get(task.toolCallId) : undefined} />
      </li>
    ))
  }

  const groups = groupBackgroundTasks(tasks, toolNames)
  return groups.map((group, index) => (
    <BackgroundToolGroupItem
      key={group.key}
      group={group}
      separator={hasLeadingTools || index > 0}
      stopControl={stopControl}
    />
  ))
}
