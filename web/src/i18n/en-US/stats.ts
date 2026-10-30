import type { Messages } from '..'
import { plural } from '../format'
import { formatCompact, formatCount as grouped } from './format'

const oneDecimal = (value: number) => grouped(Math.round(value * 10) / 10)
const n = (count: number, one: string, other: string) => `${grouped(count)} ${plural('en-US', count, { one, other })}`
const capitalize = (text: string) => text.charAt(0).toUpperCase() + text.slice(1)

export const stats = {
  title: 'Stats',
  global: 'Global',
  platform: 'Platform',
  rangeLabel: 'Time range',
  ranges: { today: 'Today', week: 'Last 7 days', month: 'Last 30 days' },
  scopeLabel: 'Scope',
  allProjects: 'All projects',
  projectGroup: 'Projects',
  missingProject: (name: string) => `${name} (not found)`,
  today: 'Today',
  day: (month: number, date: number) => `${month}/${date}`,
  shortDay: (date: number) => String(date),
  hour: (hour: number) => `${hour}:00`,
  window: (from: string, to: string) => `${from} – ${to}`,
  empty: 'Nothing recorded in this period',
  none: '—',
  total: 'Total',

  // 图表按“万”计算；英文改写成 K、M。不到 1,000 时直接写 token 数。
  wan: (tokens: number) => (tokens < 1000 ? n(tokens, 'token', 'tokens') : formatCompact(tokens)),
  wanAxis: (wan: number) => formatCompact(Math.round(wan * 10) * 1000),
  number: (value: number) => grouped(value),
  count: (value: number) => grouped(value),
  items: (value: number) => grouped(value),
  percent: (rate: number) => `${oneDecimal(rate * 100)}%`,
  wholePercent: (rate: number) => `${Math.round(rate * 100)}%`,
  usd: (usd: number) => `$${usd < 10 ? usd.toFixed(2) : grouped(Math.round(usd))}`,
  seconds: (s: number) => (s < 60 ? `${grouped(Math.round(s))} s` : s < 3600 ? `${oneDecimal(s / 60)} min` : `${oneDecimal(s / 3600)} h`),
  clock: (s: number) => {
    const total = Math.round(s)
    if (total < 60) return `${total} s`
    if (total < 3600) return total % 60 ? `${Math.floor(total / 60)} min ${total % 60} s` : `${total / 60} min`
    const minutes = Math.round((total % 3600) / 60)
    return minutes ? `${Math.floor(total / 3600)} h ${minutes} min` : `${Math.floor(total / 3600)} h`
  },
  minutes: (s: number) => (s < 60 ? 'under 1 min' : s < 7200 ? `${Math.round(s / 60)} min` : `${oneDecimal(s / 3600)} h`),

  kpis: {
    label: 'Key metrics',
    unrequested: 'Spend nobody asked for',
    unrequestedDelta: (wakes: number, tokens: string) => `${n(wakes, 'scheduled wake', 'scheduled wakes')}, ${tokens}`,
    context: 'Avg context per reply',
    contextDelta: (compactions: number, top: { name: string; compactions: number } | null) =>
      top ? `${n(compactions, 'compaction', 'compactions')}, ${grouped(top.compactions)} by ${top.name}` : n(compactions, 'compaction', 'compactions'),
    wait: 'Time to first reply, p50',
    waitDelta: (p90: string) => `p90 ${p90}`,
    failures: 'Failed + interrupted',
    failuresDelta: (failed: number, interrupted: number) => `${grouped(failed)} failed, ${grouped(interrupted)} interrupted`,
    questions: 'Waiting for your answer',
    questionsDelta: (avg: string | null, pending: number) =>
      capitalize([avg && `you reply in ${avg} on average`, pending ? `${grouped(pending)} waiting now` : 'none waiting now'].filter(Boolean).join(', ')),
    tokens: 'Total tokens',
    tokensDelta: (cost: string | null, hit: string | null) =>
      capitalize([cost && `est. ${cost}`, hit && `${hit} cache hits`].filter(Boolean).join(', ')),
  },

  origins: {
    title: (hourly: boolean) => (hourly ? 'Hourly usage by trigger' : 'Daily usage by trigger'),
    unit: 'Tokens',
    console: 'Your messages',
    sleepWake: 'Scheduled wakes',
    agents: 'Agent-to-Agent / tasks',
    other: 'Other',
    label: (share: string | null) => `Stacked bars of token usage per period by trigger${share ? `; scheduled wakes are ${share}` : ''}`,
  },

  wakes: {
    title: 'Who burns tokens after waking',
    summary: (count: number, tokens: string) => `${n(count, 'scheduled wake', 'scheduled wakes')}, ${tokens} in total`,
    once: 'Woke only once',
    every: (interval: string) => `Wakes every ${interval}`,
    everyMinutes: (count: number) => `${count} min`,
    everyHours: (count: number) => `${count} h`,
    everyDays: (count: number) => (count === 1 ? 'day' : `${count} days`),
    row: (cadence: string, avg: string) => `${cadence}, ${avg} each time`,
    note: (name: string, avg: string, times: number | null) =>
      times ? `${name} spends ${avg} per wake on average, ${times}× other Agents.` : `${name} spends ${avg} per wake on average.`,
    prompt: 'View its prompt',
    none: 'No scheduled wakes in this period.',
  },

  context: {
    title: 'Context per reply',
    unit: 'Input sent with each model call, including cache reads (tokens)',
    compactions: (count: number) => n(count, 'compaction', 'compactions'),
    reopen: 'New session due',
    reopenTitle: 'Average context above 120K tokens',
    note: 'Above 120K, consider having it start a new session. More compactions mean it forgets earlier context more often.',
    none: 'No model calls in this period.',
  },

  questions: {
    title: 'Waiting for your answer',
    unit: 'Questions Agents asked you',
    asked: (range: string) => `Asked · ${range}`,
    avg: 'Your average reply time',
    max: 'Longest wait',
    pending: 'Still waiting',
    waited: (time: string) => `Waited ${time}`,
    noPending: 'No questions are waiting for you.',
  },

  wait: {
    title: 'Time to first reply',
    p50: 'Half within (p50)',
    p90: '90% within (p90)',
    p50Short: 'p50',
    p90Short: 'p90',
    note: 'A high p90 usually means queueing: the Agent was busy with something else while your message waited.',
    label: 'Wait from sending your message to the Agent starting its reply, p50 and p90 lines',
    none: "You didn't send any messages in this period.",
  },

  outcomes: {
    title: 'Reply outcomes',
    unit: (hourly: boolean) => (hourly ? 'Turns per hour' : 'Turns per day'),
    completed: 'Completed',
    interrupted: 'Interrupted / stopped',
    failed: 'Failed',
    note: (range: string, runs: number, rate: string | null) => `${range}: ${n(runs, 'turn', 'turns')}${rate ? `, ${rate} completed` : ''}.`,
    label: (rate: string | null) => `Stacked bars of reply outcomes per period${rate ? `; ${rate} completed` : ''}`,
  },

  reasons: {
    title: "Why replies didn't finish",
    total: (count: number) => `${grouped(count)} total`,
    kinds: { interrupted: 'Interrupted', stopped: 'Stopped', failed: 'Failed' },
    codes: {
      user_interrupt: 'You sent an interrupt',
      user_stopped: 'You pressed Stop',
      server_restart: 'Server restarted mid-reply',
      abort_timeout: 'Killed after the interrupt timed out',
      credential_unavailable: 'Credential unavailable',
      cli_install_failed: 'CLI install failed',
      workspace_sync_failed: 'Workspace sync failed',
      session_missing: 'Session file missing',
      pi_start_failed: 'Pi failed to start',
      pi_exited: 'Pi process exited unexpectedly',
      pi_error: 'The model service kept failing after retries',
      internal_error: 'Internal server error',
      plugin_blocked: 'A plugin blocked this turn',
      image_unsupported: "The model doesn't support images",
    } as Record<string, string>,
    noCode: 'No reason recorded',
    note: (failed: number, count: number, text: string) => `${grouped(count)} of ${n(failed, 'failure', 'failures')}: ${text}.`,
    none: 'Every reply in this period completed.',
  },

  more: 'More · for troubleshooting',

  health: {
    title: 'Agent health',
    label: 'Health metrics per Agent',
    columns: {
      name: 'Agent',
      runs: 'Replies',
      completion: 'Completed',
      waitP90: 'Wait p90',
      durationP50: 'Turn p50',
      wakes: 'Wakes',
      wakeTokens: 'Wake spend',
      context: 'Avg context',
      compactions: 'Compactions',
      tokens: 'Tokens',
      cache: 'Cache hits',
    },
    none: 'No Agents worked in this period.',
  },

  tools: {
    title: 'Tool calls',
    unit: 'Calls · failure rate',
    failureRate: (name: string, rate: string) => `${name} failure rate ${rate}`,
    note: (avg: number, max: number) => `${oneDecimal(avg)} calls per turn on average; the busiest turn made ${grouped(max)}.`,
    none: 'No tool calls in this period.',
  },

  process: {
    title: 'Models and processes',
    retries: 'Model retries',
    piExits: 'Pi crashes',
    piExitsNote: 'Includes forced stops after an interrupt timed out',
    coldStarts: 'Pi cold starts',
    coldStartAvg: (seconds: string) => `avg ${seconds}`,
    coldStartSeconds: (ms: number) => `${oneDecimal(ms / 1000)} s`,
    byModel: 'Tokens by model',
    share: (family: string, share: string) => `${family} ${share}`,
    others: (shares: string[]) => shares.join(', '),
    families: { claude: 'Claude', gpt: 'GPT', other: 'Other' },
    cost: 'Estimated cost',
    costNote: 'At API list prices; for reference only',
    unpriced: (models: string[]) => `Excludes models without a price: ${models.map((m) => m || 'unknown model').join(', ')}`,
    noPrice: 'No models to estimate',
  },

  busy: {
    title: 'How busy the day gets',
    unit: 'Agents replying at once',
    series: 'Avg concurrent replies',
    peak: (hour: number, value: string) => `${hour}:00 · ${value}`,
    value: (value: number) => oneDecimal(value),
    label: (peak: string | null) => `Average concurrent replies by hour${peak ? `; peak at ${peak}` : ''}`,
  },
} satisfies Messages['stats']
