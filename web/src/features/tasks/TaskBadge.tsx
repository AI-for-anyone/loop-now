import type { Schemas } from '@/api/client'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'
export function TaskBadge({ status }: { status: Schemas['TaskStatus'] | Schemas['TaskNodeStatus'] }) {
  const labels = { ...t.tasks.statuses, ...t.tasks.nodeStatuses }
  const tone = status === 'blocked' ? 'danger' : status === 'waiting' ? 'warning' : status === 'running' || status === 'submitted' ? 'busy' : status === 'completed' ? 'success' : 'neutral'
  return <Badge tone={tone}>{labels[status]}</Badge>
}
