import { Popover as PopoverPrimitive } from 'radix-ui'
import type { Schemas } from '@/api/client'
import { badgeVariants } from '@/components/ui/badge'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { BackgroundKindIcon } from './BackgroundTasks'
import { BackgroundTaskBody, BackgroundTaskHead, BackgroundTaskPopover, BackgroundToolChip } from './BackgroundToolChip'

type Task = Schemas['BackgroundTask']

const MAX_CHIPS = 2

const toolNameOf = (task: Task, toolNames?: ReadonlyMap<string, string>) => (task.toolCallId ? toolNames?.get(task.toolCallId) : undefined)

// Overflow chip "+N" when more than two ended tasks triggered the follow-up reply: opens a popover with the rest,
// using the same detail layout as a single task chip.
function MoreBackgroundTasksChip({ tasks, toolNames }: { tasks: Task[]; toolNames?: ReadonlyMap<string, string> }) {
  const copy = t.background.followUp
  const title = copy.moreTitle(tasks.length)
  return (
    <PopoverPrimitive.Root>
      <PopoverPrimitive.Trigger
        type="button"
        data-background-follow-up-more={tasks.length}
        aria-label={copy.moreLabel(tasks.length)}
        title={copy.moreLabel(tasks.length)}
        className={cn(
          badgeVariants({ variant: 'chip' }),
          'cursor-pointer bg-surface-subtle font-mono text-ink-700 outline-none hover:border-hairline-strong focus-visible:shadow-focus aria-expanded:border-ink',
        )}
      >
        {copy.more(tasks.length)}
      </PopoverPrimitive.Trigger>
      <BackgroundTaskPopover label={title}>
        <div className="flex shrink-0 items-center border-b border-hairline bg-surface-subtle px-4 py-2.5">
          <span className="text-[13.5px] font-semibold text-ink">{title}</span>
        </div>
        <ul className="flex min-h-0 flex-1 flex-col overflow-y-auto">
          {tasks.map((task) => {
            const name = toolNameOf(task, toolNames) ?? t.background.kind[task.kind]
            return (
              <li key={task.id} data-background-tool={task.id} className="flex flex-col gap-2.5 border-b border-hairline px-4 py-3 last:border-b-0">
                <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                  <BackgroundTaskHead task={task} name={name} />
                </div>
                <BackgroundTaskBody task={task} />
              </li>
            )
          })}
        </ul>
      </BackgroundTaskPopover>
    </PopoverPrimitive.Root>
  )
}

// In place of a person's message, above a reply the CLI wrote by itself after background work ended (origin background):
// one line with the kind icon, the heading, up to two compact chips for the ended tasks (then "+N" opening the rest),
// and the time. Full commands live in the chip popovers.
export function BackgroundFollowUpLine({ tasks, runtimeName, at, toolNames }: { tasks: Task[]; runtimeName: string; at: string; toolNames?: ReadonlyMap<string, string> }) {
  const copy = t.background.followUp
  const first = tasks[0]
  const shown = tasks.slice(0, MAX_CHIPS)
  const rest = tasks.slice(MAX_CHIPS)
  return (
    <div role="note" data-background-follow-up className="flex min-w-0 items-start gap-2.5">
      <span aria-hidden="true" className="mt-px grid size-6 shrink-0 place-items-center rounded-[7px] border border-hairline bg-surface-subtle text-ink">
        <BackgroundKindIcon kind={first?.kind ?? 'command'} className="size-3.5" />
      </span>
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1.5 text-[13.5px] leading-6 font-medium text-ink-700">
        <span>{first ? copy.heading(runtimeName) : copy.unknown(runtimeName)}</span>
        {shown.map((task) => (
          <BackgroundToolChip key={task.id} task={task} toolName={toolNameOf(task, toolNames)} />
        ))}
        {rest.length > 0 && <MoreBackgroundTasksChip tasks={rest} toolNames={toolNames} />}
        <time dateTime={at} className="shrink-0 text-[12px] font-medium text-ink-500">
          {t.chat.time(at)}
        </time>
      </div>
    </div>
  )
}
