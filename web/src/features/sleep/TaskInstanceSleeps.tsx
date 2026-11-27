import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { SleepUntil } from './SleepUntil'

export function TaskInstanceSleeps({ detail }: { detail: Schemas['TaskDetail'] }) {
  const nodeSleeps = new Set(detail.nodes.map((node) => node.sleep?.id).filter(Boolean))
  const instances = detail.sleepingInstances.filter(
    (sleep) => sleep.scopeKind === 'task' && sleep.taskId === detail.task.id && sleep.state === 'sleeping' && sleep.runtimeId && !nodeSleeps.has(sleep.id),
  )
  if (!instances.length) return null
  return (
    <section
      aria-label={t.sleep.instances}
      className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1.5 border-b border-hairline bg-surface-subtle/60 px-4 py-2 text-[12px] sm:px-6"
    >
      <h2 className="shrink-0 text-[11.5px] font-semibold text-ink-500">{t.sleep.instances}</h2>
      <ul className="flex min-w-0 flex-1 flex-wrap items-center gap-x-4 gap-y-1">
        {instances.map((sleep) => (
          <li key={sleep.runtimeId} className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
            <span className="font-semibold text-ink">
              {detail.nodes.find((node) => node.runtimeId === sleep.runtimeId)?.agentName ?? sleep.agentId}
            </span>
            <span className="font-mono text-[11px] text-ink-500 break-all">{t.sleep.instance(sleep.runtimeId!)}</span>
            {sleep.wakeAt && <SleepUntil compact at={sleep.wakeAt} reason={sleep.reason} />}
          </li>
        ))}
      </ul>
    </section>
  )
}
