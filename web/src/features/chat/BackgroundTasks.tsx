import { BotIcon, ChevronDownIcon, CircleDashedIcon, LoaderCircleIcon, RotateCwIcon, SquareTerminalIcon } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { Link } from 'react-router'
import { Popover as PopoverPrimitive } from 'radix-ui'
import { ApiError, type Schemas } from '@/api/client'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { describeError } from '@/lib/errors'
import { useNow } from '@/lib/use-now'
import { cn } from '@/lib/utils'
import { useQueueActions } from './queue-actions'
import { elapsedClock } from './time'

type Task = Schemas['BackgroundTask']

const KIND_ICON = { command: SquareTerminalIcon, subagent: BotIcon, other: CircleDashedIcon } satisfies Record<Task['kind'], unknown>

export function BackgroundKindIcon({ kind, className }: { kind: Task['kind']; className?: string }) {
  const Icon = KIND_ICON[kind] ?? CircleDashedIcon
  return <Icon aria-hidden="true" className={className} strokeWidth={2.4} />
}

// Where the reply that started a task is: a link to it in the conversation, with its number or its time.
export interface TaskOrigin {
  to: string
  label: string
}

// Stopping background tasks: one, or all of them. Each request carries an actionId; when its outcome is unknown, the button turns into a
// retry with the same actionId (as stopping a reply does).
export interface BackgroundStopper {
  stop: (taskId: string, actionId: string) => Promise<unknown>
  stopAll: (actionId: string) => Promise<unknown>
}

// The request was answered with 409: the task (or every task) had already ended, which is what the person wanted.
const alreadyEnded = (error: unknown) => error instanceof ApiError && error.status === 409

function useBackgroundStop(stopper: BackgroundStopper) {
  const actions = useQueueActions()
  // Tasks whose stop was accepted; they show “正在停止…” (Stopping…) until the running list drops them.
  const [requested, setRequested] = useState<ReadonlySet<string>>(() => new Set())
  const [errors, setErrors] = useState<Record<string, unknown>>({})
  const settle = (key: string, ids: string[], result: Awaited<ReturnType<typeof actions.run>>) => {
    if (!result) return
    const failed = !result.ok && !alreadyEnded(result.error)
    setErrors((old) => ({ ...old, [key]: failed ? result.error : undefined }))
    if (!failed) setRequested((old) => new Set([...old, ...ids]))
  }
  return {
    stop: async (taskId: string) => settle(taskId, [taskId], await actions.run(taskId, 'stop', (actionId) => stopper.stop(taskId, actionId))),
    stopAll: async (ids: string[]) => settle('all', ids, await actions.run('all', 'stop', (actionId) => stopper.stopAll(actionId))),
    stopping: (task: Task) => actions.pending.has(task.id) || actions.pending.has('all') || requested.has(task.id) || Boolean(task.stopRequestedAt),
    retry: (key: string) => actions.isUnknown(key, 'stop'),
    error: (key: string) => errors[key],
  }
}

// The running background tasks of an Agent (or of a task's node): what each one is, which reply started it, how long it has run, and Stop;
// Stop all below when there are several. Shown in the panel of the header mark, and in a task node's header.
export function BackgroundTaskList({ tasks, stopper, origin, onNavigate }: { tasks: Task[]; stopper: BackgroundStopper; origin?: (task: Task) => TaskOrigin | null; onNavigate?: () => void }) {
  const now = useNow(1000, tasks.length > 0)
  const control = useBackgroundStop(stopper)
  if (tasks.length === 0) return <p role="status" className="px-4 py-3 text-[13.5px] font-bold text-ink-500">{t.background.allEnded}</p>
  const allError = control.error('all')
  return (
    <div className="flex min-w-0 flex-col">
      <ul aria-label={t.background.title} className="flex min-w-0 flex-col">
        {tasks.map((task) => {
          const from = origin?.(task) ?? null
          const stopping = control.stopping(task)
          const error = control.error(task.id)
          const clock = elapsedClock(now - Date.parse(task.startedAt))
          return (
            <li key={task.id} data-background-task={task.id} className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-x-3 border-b-[1.5px] border-dashed border-ink/25 px-4 py-3 last:border-b-0">
              <span aria-hidden="true" className="mt-0.5 grid size-8 place-items-center rounded-[9px] bg-ink text-riso-yellow shadow-[2px_2px_0_var(--riso-pink)]">
                <BackgroundKindIcon kind={task.kind} className="size-[17px]" />
              </span>
              <div className="flex min-w-0 flex-col gap-1">
                <p title={task.label} className="line-clamp-2 font-mono text-[13px] leading-[1.45] font-bold [overflow-wrap:anywhere] text-ink">
                  {task.label}
                </p>
                <p className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-[12px] leading-5 text-ink-500">
                  <span className="font-bold">{t.background.kind[task.kind]}</span>
                  {from && (
                    <>
                      <span aria-hidden="true">{t.chat.metaSeparator}</span>
                      <Link to={from.to} onClick={onNavigate} aria-label={t.background.fromReplyLabel(from.label)} className="font-bold text-riso-blue underline decoration-[1.5px] underline-offset-[3px] hover:decoration-riso-pink">
                        {from.label}
                      </Link>
                    </>
                  )}
                </p>
                {error !== undefined && (
                  <p role="alert" className="text-[12.5px] font-bold text-red-text">
                    {describeError(error)}
                  </p>
                )}
              </div>
              <div className="flex flex-col items-end gap-1.5">
                <span className="inline-flex items-center gap-1.5 font-mono text-[15px] leading-5 font-black tabular-nums text-ink" title={t.background.elapsedLabel(clock)}>
                  <LoaderCircleIcon aria-hidden="true" className="riso-spin size-3.5 text-pink-text motion-reduce:animate-none" strokeWidth={3} />
                  <span className="sr-only">{t.background.elapsedLabel(clock)}</span>
                  <span aria-hidden="true">{clock}</span>
                </span>
                <Button
                  type="button"
                  size="xs"
                  variant="danger-outline"
                  // Stopping is a state of its own, not a greyed-out button: a dashed red pill with a spinner until the list drops the task.
                  className={cn('h-7 px-3 text-[12.5px]', stopping && 'border-dashed bg-tint-red disabled:opacity-100')}
                  disabled={stopping}
                  aria-label={stopping ? t.background.stopping : t.background.stopLabel(task.label)}
                  onClick={() => void control.stop(task.id)}
                >
                  {stopping && <LoaderCircleIcon aria-hidden="true" className="riso-spin size-3 motion-reduce:animate-none" strokeWidth={3} />}
                  {stopping ? t.background.stopping : control.retry(task.id) ? t.background.retryStop : t.background.stop}
                </Button>
              </div>
            </li>
          )
        })}
      </ul>
      {tasks.length > 1 && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-t-2 border-ink bg-tint-red/40 px-4 py-2.5">
          <Button type="button" size="xs" variant="destructive" className="h-8" disabled={tasks.every(control.stopping)} onClick={() => void control.stopAll(tasks.map((task) => task.id))}>
            {control.retry('all') ? t.background.retryStop : t.background.stopAll(tasks.length)}
          </Button>
          <span className="min-w-0 flex-1 text-[12px] leading-snug text-ink-700">{t.background.stopAllHint}</span>
          {allError !== undefined && <ErrorAlert error={allError} className="basis-full" />}
        </div>
      )}
    </div>
  )
}

// The mark next to the Agent's state while background tasks run: “后台任务 N” (Background N), a yellow tilted pill with a spinning
// mark, so an idle Agent with work still going does not look finished. It opens the list.
// hint: what the list is about (by default, that the reply has ended and these still run).
export function BackgroundTasksMark({ tasks, stopper, origin, hint, className }: { tasks: Task[]; stopper: BackgroundStopper; origin?: (task: Task) => TaskOrigin | null; hint?: string; className?: string }) {
  const [open, setOpen] = useState(false)
  if (tasks.length === 0) return null
  return (
    <PopoverPrimitive.Root open={open} onOpenChange={setOpen}>
      <PopoverPrimitive.Trigger
        data-background-mark
        aria-label={t.background.markLabel(tasks.length)}
        className={cn(
          'riso-tilt inline-flex min-h-[26px] shrink-0 cursor-pointer items-center gap-1.5 rounded-full px-3 text-[12.5px] leading-[26px] font-black whitespace-nowrap text-ink outline-none [--tilt-bg:var(--riso-yellow)] [--tilt-shadow:inset_0_0_0_2px_var(--ink),2px_2px_0_var(--ink)] [--tilt:2deg] focus-visible:shadow-focus',
          className,
        )}
      >
        <LoaderCircleIcon aria-hidden="true" className="riso-spin size-3.5 motion-reduce:animate-none" strokeWidth={3} />
        <span>{t.background.mark(tasks.length)}</span>
        <ChevronDownIcon aria-hidden="true" className={cn('size-3.5 transition-transform motion-reduce:transition-none', open && 'rotate-180')} strokeWidth={3} />
      </PopoverPrimitive.Trigger>
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Content
          align="start"
          sideOffset={10}
          collisionPadding={12}
          aria-label={t.background.title}
          className="z-50 flex max-h-[min(520px,calc(100dvh-120px))] w-[min(440px,calc(100vw-24px))] flex-col overflow-hidden rounded-card bg-paper-raised text-ink shadow-[0_0_0_2px_var(--ink),6px_6px_0_2px_var(--riso-yellow)] outline-none"
        >
          <BackgroundPanelHead count={tasks.length} hint={hint} />
          <div className="min-h-0 flex-1 overflow-y-auto">
            <BackgroundTaskList tasks={tasks} stopper={stopper} origin={origin} onNavigate={() => setOpen(false)} />
          </div>
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  )
}

export function BackgroundPanelHead({ count, hint = t.background.hint, children }: { count: number; hint?: string; children?: ReactNode }) {
  return (
    <div className="flex shrink-0 flex-wrap items-baseline gap-x-2.5 gap-y-0.5 border-b-2 border-ink bg-tint-yellow px-4 py-2.5">
      <span className="font-display-cn text-[20px] leading-tight">{t.background.title}</span>
      <span className="rounded-full bg-ink px-2 text-[12px] leading-5 font-black text-riso-yellow">{t.background.running(count)}</span>
      <span className="basis-full text-[12.5px] font-bold text-ink-700">{hint}</span>
      {children}
    </div>
  )
}

// Above the message box when a settings change takes effect with the next message, which restarts the CLI and so stops its running
// background tasks: said before the person sends it.
export function RestartNotice({ runtimeName, count }: { runtimeName: string; count: number }) {
  return (
    <p role="status" data-restart-notice className="flex min-w-0 items-start gap-2 rounded-ticket border-2 border-dashed border-red-text/60 bg-tint-red px-3 py-1.5 text-[13px] leading-snug font-bold text-red-text">
      <RotateCwIcon aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" strokeWidth={2.8} />
      <span className="min-w-0">{t.background.restartWarning(runtimeName, count)}</span>
    </p>
  )
}

// The mark in lists (overview, project Agents) on an idle Agent whose background tasks still run: a small yellow tilted tag
// “后台任务 N” (Background N) with the spinner, next to its idle state.
export function BackgroundCountTag({ count, className }: { count: number; className?: string }) {
  if (count <= 0) return null
  return (
    <span data-background-count={count} title={t.background.listTitle(count)} className={cn('relative inline-flex min-h-6 shrink-0 items-center px-[10px] text-[12px] font-black whitespace-nowrap text-ink', className)}>
      <span aria-hidden="true" className="absolute inset-0 rotate-[2deg] rounded-full bg-riso-yellow shadow-[inset_0_0_0_2px_var(--ink)]" />
      <span className="relative inline-flex items-center gap-1.5">
        <LoaderCircleIcon aria-hidden="true" className="riso-spin size-3 motion-reduce:animate-none" strokeWidth={3} />
        {t.background.mark(count)}
      </span>
    </span>
  )
}
