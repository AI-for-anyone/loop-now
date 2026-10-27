import type { Schemas } from '@/api/client'

export type UsageRange = 'today' | 'week' | 'month'
export type UsageDimension = 'agent' | 'model'

export const USAGE_RANGES: UsageRange[] = ['today', 'week', 'month']
export const USAGE_DIMENSIONS: UsageDimension[] = ['agent', 'model']

// 时间段的开始时刻，按浏览器本地时区计算：今天从零点起，本周从周一零点起，近 30 天为当前时刻往前 30×24 小时。
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

// 按所选维度汇总的一行。按 Agent 时 deleted 标明 Agent 已删除；按模型时 provider 与 model 都为 null 表示未知模型。
export interface UsageLine extends UsageParts {
  key: string
  agentName: string | null
  // 按 Agent 时的所属，用于“全部”时的所属标签；按模型时为 null。
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

// 服务端按（Agent、提供方、模型）分组，两种视图都在这里由同一份结果汇总，按合计从大到小排列。
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
