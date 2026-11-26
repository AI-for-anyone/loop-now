import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { BackgroundKindIcon } from './BackgroundTasks'
import { endSummary } from './background-text'

type Task = Schemas['BackgroundTask']

const PILL: Record<Task['status'], string> = {
  running: 'bg-tint-yellow text-ink [[data-skin=plate]_&]:bg-riso-yellow',
  completed: 'bg-tint-green text-green-text',
  failed: 'bg-tint-red text-red-text',
  stopped: 'border border-hairline-strong text-ink-700 [[data-skin=plate]_&]:border-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/40',
  unknown: 'bg-surface-active text-ink-700 [[data-skin=plate]_&]:bg-ink/[0.07]',
}

// In place of a person's message, above a reply the CLI wrote by itself after background work ended (origin background):
// “后台任务「npm test」结束后，Claude Code 接着说：” (After background task “npm test” ended, Claude Code went on:), with how it ended.
// Several tasks name the first and how many more; all of them are in the tooltip.
export function BackgroundFollowUpLine({ tasks, runtimeName, at }: { tasks: Task[]; runtimeName: string; at: string }) {
  const copy = t.background.followUp
  const first = tasks[0]
  const tip = tasks.map((task) => `${task.label} — ${endSummary(task)}`).join('\n')
  return (
    <div role="note" data-background-follow-up title={tip || undefined} className="flex min-w-0 items-start gap-2.5">
      <span aria-hidden="true" className="mt-px grid size-6 shrink-0 place-items-center rounded-[7px] border border-hairline bg-surface-subtle text-ink [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-ink [[data-skin=plate]_&]:text-riso-yellow [[data-skin=plate]_&]:shadow-[2px_2px_0_var(--riso-yellow)]">
        <BackgroundKindIcon kind={first?.kind ?? 'command'} className="size-3.5" />
      </span>
      <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[13.5px] leading-6 font-medium text-ink-700 [[data-skin=plate]_&]:font-bold">
        <span className="min-w-0 [overflow-wrap:anywhere]">
          {first ? (
            <>
              {copy.before}
              <span className="riso-marker px-0.5 font-mono text-[12.5px] font-medium text-ink [[data-skin=plate]_&]:font-bold">{first.label}</span>
              {tasks.length > 1 ? copy.afterMany(tasks.length - 1, runtimeName) : copy.after(runtimeName)}
            </>
          ) : (
            copy.unknown(runtimeName)
          )}
        </span>
        {first && first.status !== 'running' && (
          <span className={cn('inline-flex min-h-[20px] shrink-0 items-center rounded-full px-2 text-[11.5px] leading-5 font-semibold whitespace-nowrap [[data-skin=plate]_&]:font-black', PILL[first.status])}>{endSummary(first)}</span>
        )}
        <time dateTime={at} className="shrink-0 text-[12px] font-medium text-ink-500">
          {t.chat.time(at)}
        </time>
      </p>
    </div>
  )
}
