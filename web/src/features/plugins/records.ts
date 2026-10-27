import type { Schemas } from '@/api/client'

// 投递记录（plugins/SPEC.md §7）：插件经 SDK 写在状态键 deliveries/<ULID> 下，值是下面的形状。
// 读出时认得这个形状的按列显示，新的在前（按 at）；认不得的原样显示 JSON。
export type DeliveryRecord = {
  id: string
  at: string
  eventId: string
  eventType: string
  target: string
  status: 'pending' | 'delivered' | 'failed' | 'abandoned'
  attempts: number
  nextAttemptAt: string | null
  lastStatusCode: number | null
  lastError: string | null
  updatedAt: string
}

export type RecordRow = { key: string; record: DeliveryRecord } | { key: string; raw: unknown }

export const RECORD_PREFIX = 'deliveries/'

const statuses = new Set(['pending', 'delivered', 'failed', 'abandoned'])

function asRecord(value: unknown): DeliveryRecord | null {
  if (typeof value !== 'object' || value === null) return null
  const v = value as Record<string, unknown>
  if (typeof v.at !== 'string' || typeof v.target !== 'string' || typeof v.status !== 'string' || !statuses.has(v.status) || typeof v.attempts !== 'number') return null
  return {
    id: typeof v.id === 'string' ? v.id : '',
    at: v.at,
    eventId: typeof v.eventId === 'string' ? v.eventId : '',
    eventType: typeof v.eventType === 'string' ? v.eventType : '',
    target: v.target,
    status: v.status as DeliveryRecord['status'],
    attempts: v.attempts,
    nextAttemptAt: typeof v.nextAttemptAt === 'string' ? v.nextAttemptAt : null,
    lastStatusCode: typeof v.lastStatusCode === 'number' ? v.lastStatusCode : null,
    lastError: typeof v.lastError === 'string' ? v.lastError : null,
    updatedAt: typeof v.updatedAt === 'string' ? v.updatedAt : v.at,
  }
}

// 认得的按 at 新的在前；认不得的排在后面，按键名。
export function recordRows(entries: Schemas['PluginStateEntry'][]): RecordRow[] {
  const known: { key: string; record: DeliveryRecord }[] = []
  const raw: { key: string; raw: unknown }[] = []
  for (const entry of entries) {
    const record = asRecord(entry.value)
    if (record) known.push({ key: entry.key, record })
    else raw.push({ key: entry.key, raw: entry.value })
  }
  known.sort((a, b) => b.record.at.localeCompare(a.record.at))
  return [...known, ...raw]
}
