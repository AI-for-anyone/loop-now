import type { Schemas } from '@/api/client'

export type UsageRange = 'today' | 'week' | 'month'
export type UsageDimension = 'agent' | 'model'

export const USAGE_RANGES: UsageRange[] = ['today', 'week', 'month']
export const USAGE_DIMENSIONS: UsageDimension[] = ['agent', 'model']

// The start of the period, in the browser's local time zone: today starts at midnight, this week at midnight on Monday, and the
// last 30 days go back 30×24 hours from now.
export function usageRangeStart(range: UsageRange, now: Date = new Date()): Date {
  switch (range) {
    case 'today':
      return new Date(now.getFullYear(), now.getMonth(), now.getDate())
    case 'week':
      return new Date(now.getFullYear(), now.getMonth(), now.getDate() - ((now.getDay() + 6) % 7))
    case 'month':
      return new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000)
  }
}

export interface UsageParts {
  input: number
  output: number
  cacheRead: number
  cacheWrite: number
  total: number
}

// One row summed by the selected dimension. By Agent, deleted marks a deleted Agent; by model, provider and model both null
// means an unknown model.
export interface UsageLine extends UsageParts {
  key: string
  agentName: string | null
  // By Agent, where the Agent belongs, used for the ownership tag when “全部” (All) is selected; null by model.
  scope: Schemas['AgentScope'] | null
  projectId: string | null
  deleted: boolean
  provider: string | null
  model: string | null
}

const emptyParts = (): UsageParts => ({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 })

function add(target: UsageParts, row: UsageParts) {
  target.input += row.input
  target.output += row.output
  target.cacheRead += row.cacheRead
  target.cacheWrite += row.cacheWrite
  target.total += row.total
}

export function sumUsage(rows: Schemas['UsageRow'][]): UsageParts {
  const sum = emptyParts()
  for (const row of rows) add(sum, row)
  return sum
}

// The server groups by (Agent, provider, model); both views are summed here from the same result and sorted by total,
// largest first.
export function aggregateUsage(rows: Schemas['UsageRow'][], dimension: UsageDimension): UsageLine[] {
  const lines = new Map<string, UsageLine>()
  for (const row of rows) {
    const key = dimension === 'agent' ? row.agentId : row.provider === null || row.model === null ? '' : `${row.provider}\u0000${row.model}`
    let line = lines.get(key)
    if (!line) {
      line =
        dimension === 'agent'
          ? { key, agentName: row.agentName, scope: row.scope, projectId: row.projectId, deleted: row.agentDeleted, provider: null, model: null, ...emptyParts() }
          : { key, agentName: null, scope: null, projectId: null, deleted: false, provider: key ? row.provider : null, model: key ? row.model : null, ...emptyParts() }
      lines.set(key, line)
    }
    add(line, row)
  }
  return [...lines.values()].sort((a, b) => b.total - a.total)
}
