import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { SleepUntil } from './SleepUntil'

export function TaskInstanceSleeps({ detail }: { detail: Schemas['TaskDetail'] }) {
  const nodeSleeps = new Set(detail.nodes.map(node => node.sleep?.id).filter(Boolean))
  const instances = detail.sleepingInstances.filter(sleep => sleep.scopeKind === 'task' && sleep.taskId === detail.task.id && sleep.state === 'sleeping' && sleep.runtimeId && !nodeSleeps.has(sleep.id))
  if (!instances.length) return null
  return <section aria-label={t.sleep.instances} className="my-4 grid min-w-0 gap-3 rounded-panel bg-tint-blue p-4">
    <h2 className="font-bold">{t.sleep.instances}</h2><ul className="grid gap-3">{instances.map(sleep => <li key={sleep.runtimeId} className="grid min-w-0 gap-1">
      <p className="font-bold break-words">{detail.nodes.find(node => node.runtimeId === sleep.runtimeId)?.agentName ?? sleep.agentId}</p>
      <p className="text-xs break-all">{t.sleep.instance(sleep.runtimeId!)}</p>
      {sleep.wakeAt && <SleepUntil at={sleep.wakeAt} reason={sleep.reason} />}
    </li>)}</ul>
  </section>
}
