import type { Schemas } from '@/api/client'
import { statsWindow } from '@/api/stats'

type Report = Schemas['StatsReport']
export type StatsAgentRef = { agentId: string; projectId: string | null }

const WAN = 10_000
const NAMES = ['web', 'server', 'manager', 'test', 'worker', 'cli'] as const
export type StatsAgentName = (typeof NAMES)[number]

// 统计页第二版设计稿（Stats.dc.html 的 renderVals）里的示意数据，按 GET /stats 的形状写出：近 7 天，按天分桶。
// agents 给出各个名称对应的 Agent ID 与所属项目，等你回答与“去看它的提示词”据此生成链接。
export function statsFixture(agents: Record<StatsAgentName, StatsAgentRef>, now: Date = new Date()): Report {
  const { from, to } = statsWindow('week', now)
  const buckets = Array.from({ length: 7 }, (_, i) => new Date(from.getFullYear(), from.getMonth(), from.getDate() + i).toISOString())
  const ref = (name: StatsAgentName) => ({ ...agents[name], name })

  const consoleWan = [250, 300, 260, 330, 290, 380, 210]
  const wakeWan = [520, 530, 520, 560, 560, 600, 430]
  const agentsWan = [90, 90, 100, 120, 120, 140, 80]
  const otherWan = [4, 3, 5, 4, 6, 5, 3]
  const origins = buckets.map((_, i) => ({
    console: consoleWan[i] * WAN,
    inboxReminder: Math.round(agentsWan[i] * 0.6) * WAN,
    sleepWake: wakeWan[i] * WAN,
    task: Math.round(agentsWan[i] * 0.4) * WAN,
    other: otherWan[i] * WAN,
  }))
  const perBucket = origins.map((o) => o.console + o.inboxReminder + o.sleepWake + o.task + o.other)
  const sum = (values: number[]) => values.reduce((a, b) => a + b, 0)
  const total = sum(perBucket)
  const originTotals = {
    console: sum(origins.map((o) => o.console)),
    inboxReminder: sum(origins.map((o) => o.inboxReminder)),
    sleepWake: sum(origins.map((o) => o.sleepWake)),
    task: sum(origins.map((o) => o.task)),
    other: sum(origins.map((o) => o.other)),
  }
  // 分项：输出 3%、缓存写入 4%，其余为输入与缓存读，缓存命中 79%。
  const output = Math.round(total * 0.03)
  const cacheWrite = Math.round(total * 0.04)
  const cacheRead = Math.round((total - output - cacheWrite) * 0.79)
  const input = total - output - cacheWrite - cacheRead

  const outcomes = [[160, 8, 3], [190, 6, 5], [175, 9, 2], [210, 7, 4], [198, 5, 6], [230, 10, 3], [168, 6, 2]].map(([completed, interrupted, failed]) => ({ completed, interrupted, failed }))
  const health = (
    name: StatsAgentName,
    runs: number,
    completionRate: number,
    waitP90: number,
    durationP50: number,
    wakes: number,
    wakeTokens: number,
    avgContext: number,
    compactions: number,
    tokens: number,
    cacheHitRate: number,
  ): Schemas['StatsAgentHealth'] => ({ ...ref(name), runs, completionRate, waitP90, durationP50, wakes, wakeTokens, avgContext, compactions, tokens, cacheHitRate })

  return {
    from: from.toISOString(),
    to: to.toISOString(),
    bucket: 'day',
    buckets,
    tokens: {
      totals: { input, output, cacheRead, cacheWrite, total },
      cacheHitRate: cacheRead / (input + cacheRead),
      byModel: [
        { provider: 'anthropic', model: 'claude-opus-5-5', total: Math.round(total * 0.61) },
        { provider: 'openai-codex', model: 'gpt-6.1-sol', total: Math.round(total * 0.29) },
        { provider: 'anthropic', model: 'claude-sonnet-5-5', total: total - Math.round(total * 0.61) - Math.round(total * 0.29) },
      ],
      series: perBucket,
    },
    byOrigin: { totals: originTotals, unrequestedShare: originTotals.sleepWake / total, series: origins },
    wakes: [
      { ...ref('web'), count: 41, tokens: 3485 * WAN, avgTokens: (3485 * WAN) / 41, avgIntervalSeconds: 4 * 3600 },
      { ...ref('server'), count: 12, tokens: 144 * WAN, avgTokens: 12 * WAN, avgIntervalSeconds: 12 * 3600 },
      { ...ref('manager'), count: 24, tokens: 72 * WAN, avgTokens: 3 * WAN, avgIntervalSeconds: 7 * 3600 },
      { ...ref('test'), count: 7, tokens: 19 * WAN, avgTokens: (19 * WAN) / 7, avgIntervalSeconds: 24 * 3600 },
    ],
    context: {
      avgInputPerCall: 91_000,
      compactions: 55,
      series: [72_000, 78_000, 81_000, 86_000, 88_000, 94_000, 91_000],
      agents: [
        { ...ref('web'), calls: 980, avgInputPerCall: 142_000, compactions: 21 },
        { ...ref('test'), calls: 520, avgInputPerCall: 98_000, compactions: 9 },
        { ...ref('server'), calls: 870, avgInputPerCall: 76_000, compactions: 12 },
        { ...ref('worker'), calls: 410, avgInputPerCall: 51_000, compactions: 6 },
        { ...ref('manager'), calls: 600, avgInputPerCall: 23_000, compactions: 4 },
        { ...ref('cli'), calls: 230, avgInputPerCall: 19_000, compactions: 3 },
      ],
    },
    questions: {
      asked: 14,
      answered: 12,
      avgAnswerSeconds: 360,
      maxAnswerSeconds: 72 * 60,
      series: [1, 3, 2, 2, 1, 3, 2],
      pending: [
        { messageId: '64000000-0000-7000-8000-000000000001', ...ref('server'), text: '要不要顺手删掉 legacy 的 agent_folder_sources 表？', askedAt: new Date(now.getTime() - 23 * 60_000).toISOString() },
        { messageId: '64000000-0000-7000-8000-000000000002', ...ref('web'), text: '侧栏宽度 244 还是 260？', askedAt: new Date(now.getTime() - 5 * 60_000).toISOString() },
      ],
    },
    wait: {
      p50: 6,
      p90: 144,
      durationP50: 50,
      series: [8, 7, 6, 6, 7, 5, 6].map((p50, i) => ({ p50, p90: [100, 130, 110, 160, 150, 170, 144][i] })),
    },
    outcomes: {
      totals: { completed: sum(outcomes.map((o) => o.completed)), interrupted: sum(outcomes.map((o) => o.interrupted)), failed: sum(outcomes.map((o) => o.failed)) },
      series: outcomes,
      reasons: [
        { kind: 'interrupted', code: 'user_interrupt', count: 30 },
        { kind: 'stopped', code: 'user_stopped', count: 16 },
        { kind: 'failed', code: 'pi_error', count: 12 },
        { kind: 'failed', code: 'pi_exited', count: 7 },
        { kind: 'failed', code: 'rate_limited', count: 3 },
        { kind: 'failed', code: 'workspace_sync_failed', count: 3 },
        { kind: 'interrupted', code: 'server_restart', count: 3 },
        { kind: 'interrupted', code: 'abort_timeout', count: 2 },
      ],
    },
    agents: [
      health('web', 312, 0.971, 108, 52, 41, 3485 * WAN, 142_000, 21, 4210 * WAN, 0.82),
      health('server', 286, 0.955, 186, 61, 12, 144 * WAN, 76_000, 12, 1180 * WAN, 0.78),
      health('manager', 198, 0.99, 24, 18, 24, 72 * WAN, 23_000, 4, 420 * WAN, 0.85),
      health('test', 176, 0.915, 252, 100, 7, 19 * WAN, 98_000, 9, 380 * WAN, 0.71),
      health('worker', 164, 0.963, 120, 44, 0, 0, 51_000, 6, 210 * WAN, 0.8),
      health('cli', 92, 0.978, 66, 35, 0, 0, 19_000, 3, 80 * WAN, 0.84),
    ],
    tools: {
      byName: [
        { name: 'bash', calls: 6120, failures: 129 },
        { name: 'read', calls: 1940, failures: 6 },
        { name: 'edit', calls: 820, failures: 38 },
        { name: 'write', calls: 410, failures: 2 },
        { name: 'lark_send', calls: 126, failures: 10 },
      ],
      callsPerRunAvg: 7.4,
      callsPerRunMax: 212,
    },
    process: { modelRetries: 111, piExits: 7, piColdStarts: 314, piColdStartAvgMs: 300 },
    concurrency: [0.2, 0.1, 0.1, 0.1, 0.1, 0.2, 0.4, 0.6, 1.2, 2.1, 2.8, 3.0, 2.2, 2.6, 3.4, 4.1, 3.8, 3.1, 2.4, 1.9, 1.6, 1.2, 0.8, 0.4],
  }
}

// 这段时间什么都没有：全零的桶，并发全为 null。
export function emptyStatsFixture(now: Date = new Date()): Report {
  const report = statsFixture(Object.fromEntries(NAMES.map((name) => [name, { agentId: '', projectId: null }])) as Record<StatsAgentName, StatsAgentRef>, now)
  const zero = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 }
  const noOrigin = { console: 0, inboxReminder: 0, sleepWake: 0, task: 0, other: 0 }
  const noOutcome = { completed: 0, interrupted: 0, failed: 0 }
  const blank = report.buckets.map(() => 0)
  return {
    ...report,
    tokens: { totals: zero, cacheHitRate: null, byModel: [], series: blank },
    byOrigin: { totals: noOrigin, unrequestedShare: null, series: report.buckets.map(() => noOrigin) },
    wakes: [],
    context: { avgInputPerCall: null, compactions: 0, series: report.buckets.map(() => null), agents: [] },
    questions: { asked: 0, answered: 0, avgAnswerSeconds: null, maxAnswerSeconds: null, series: blank, pending: [] },
    wait: { p50: null, p90: null, durationP50: null, series: report.buckets.map(() => ({ p50: null, p90: null })) },
    outcomes: { totals: noOutcome, series: report.buckets.map(() => noOutcome), reasons: [] },
    agents: [],
    tools: { byName: [], callsPerRunAvg: null, callsPerRunMax: null },
    process: { modelRetries: 0, piExits: 0, piColdStarts: 0, piColdStartAvgMs: null },
    concurrency: Array.from({ length: 24 }, () => null),
  }
}
