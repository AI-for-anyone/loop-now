import { Popover as PopoverPrimitive } from 'radix-ui'
import type { Schemas } from '@/api/client'
import { badgeVariants } from '@/components/ui/badge'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { useSkin } from '@/theme/store'
import { BackgroundKindIcon } from './BackgroundTasks'
import { BackgroundTaskBody, BackgroundTaskHead, BackgroundTaskPopover, BackgroundToolChip, BackgroundToolGroupButton } from './BackgroundToolChip'
import { groupBackgroundTasks } from './background-text'

type Task = Schemas['BackgroundTask']

const MAX_CHIPS = 2

const toolNameOf = (task: Task, toolNames?: ReadonlyMap<string, string>) => (task.toolCallId ? toolNames?.get(task.toolCallId) : undefined)

// Overflow "+N" trigger when more than two ended entries triggered the follow-up reply: opens a popover with the rest,
// using the same detail layout as a single task chip. Quiet text button in mono skin; pill in Plate skin.
function MoreBackgroundTasksChip({
  tasks,
  toolNames,
  quiet = false,
}: {
  tasks: Task[]
  toolNames?: ReadonlyMap<string, string>
  quiet?: boolean
}) {
  const copy = t.background.followUp
  const title = copy.moreTitle(tasks.length)
  return (
    <PopoverPrimitive.Root>
      <PopoverPrimitive.Trigger
        type="button"
        data-background-follow-up-more={tasks.length}
        aria-label={copy.moreLabel(tasks.length)}
        title={copy.moreLabel(tasks.length)}
        className={
          quiet
            ? '-mx-1 -my-0.5 inline-flex cursor-pointer items-center rounded-stamp px-1 py-0.5 font-mono text-[12px] font-medium tabular-nums text-ink-500 transition-colors outline-none hover:bg-surface-hover hover:text-ink focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-riso-blue aria-expanded:bg-surface-hover aria-expanded:text-ink'
            : cn(
                badgeVariants({ variant: 'chip' }),
                'cursor-pointer bg-surface-subtle font-mono text-ink-700 outline-none hover:border-hairline-strong focus-visible:shadow-focus aria-expanded:border-ink',
              )
        }
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
// one line with the kind icon, the heading, the ended tasks (quiet grouped tool-line style in mono, individual pills in Plate),
// and the time. Full commands live in the popovers.
export function BackgroundFollowUpLine({ tasks, runtimeName, at, toolNames }: { tasks: Task[]; runtimeName: string; at: string; toolNames?: ReadonlyMap<string, string> }) {
  const skin = useSkin()
  const copy = t.background.followUp
  const first = tasks[0]
  const plate = skin === 'plate'
  const shown = plate ? tasks.slice(0, MAX_CHIPS) : []
  const rest = plate ? tasks.slice(MAX_CHIPS) : []
  const groups = plate ? [] : groupBackgroundTasks(tasks, toolNames)
  const shownGroups = groups.slice(0, MAX_CHIPS)
  const restGroupTasks = groups.slice(MAX_CHIPS).flatMap((group) => group.tasks)

  return (
    <div role="note" data-background-follow-up className="flex min-w-0 items-start gap-2.5">
      <span aria-hidden="true" className="mt-px grid size-6 shrink-0 place-items-center rounded-[7px] border border-hairline bg-surface-subtle text-ink">
        <BackgroundKindIcon kind={first?.kind ?? 'command'} className="size-3.5" />
      </span>
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1.5 text-[13.5px] leading-6 font-medium text-ink-700">
        <span>{first ? copy.heading(runtimeName) : copy.unknown(runtimeName)}</span>
        {plate ? (
          <>
            {shown.map((task) => (
              <BackgroundToolChip key={task.id} task={task} toolName={toolNameOf(task, toolNames)} />
            ))}
            {rest.length > 0 && <MoreBackgroundTasksChip tasks={rest} toolNames={toolNames} />}
          </>
        ) : (
          shownGroups.length > 0 && (
            <span data-background-follow-up-tools="" className="inline-flex flex-wrap items-center gap-y-1 text-[12.5px] leading-5 font-normal text-ink-500">
              {shownGroups.map((group, index) => (
                <span key={group.key} data-background-tool-group="" data-status={group.status} className="inline-flex items-center">
                  {index > 0 && (
                    <span aria-hidden="true" data-tool-separator="" className="mx-1.5 text-ink-500/45 select-none">
                      {t.chat.metaSeparator}
                    </span>
                  )}
                  <BackgroundToolGroupButton group={group} />
                </span>
              ))}
              {restGroupTasks.length > 0 && (
                <span className="inline-flex items-center">
                  <span aria-hidden="true" data-tool-separator="" className="mx-1.5 text-ink-500/45 select-none">
                    {t.chat.metaSeparator}
                  </span>
                  <MoreBackgroundTasksChip tasks={restGroupTasks} toolNames={toolNames} quiet />
                </span>
              )}
            </span>
          )
        )}
        <time dateTime={at} className="shrink-0 text-[12px] font-medium text-ink-500">
          {t.chat.time(at)}
        </time>
      </div>
    </div>
  )
}
