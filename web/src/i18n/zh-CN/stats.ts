import { formatCount as grouped } from './format'

// 统计页（第二版设计稿）。数值与时长的写法也在这里：token 以“万”为单位，时长按大小换成秒、分、小时。
const oneDecimal = (value: number) => grouped(Math.round(value * 10) / 10)

export const stats = {
  title: '统计',
  global: '全局',
  platform: '平台',
  rangeLabel: '时间范围',
  ranges: { today: '今天', week: '近 7 天', month: '近 30 天' },
  scopeLabel: '范围',
  allProjects: '全部项目',
  projectGroup: '项目',
  missingProject: (name: string) => `${name}（找不到）`,
  today: '今天',
  day: (month: number, date: number) => `${month}/${date}`,
  shortDay: (date: number) => String(date),
  hour: (hour: number) => `${hour} 点`,
  window: (from: string, to: string) => `${from} – ${to}`,
  empty: '这段时间没有记录',
  none: '—',
  total: '合计',

  // token 用“万”：10 万以上取整，10 万以下保留一位小数；不到 1,000 时直接写 token 数。
  wan: (tokens: number) => {
    if (tokens < 1000) return `${grouped(tokens)} token`
    const wan = tokens / 10_000
    return `${wan >= 100 ? grouped(Math.round(wan)) : oneDecimal(wan)} 万`
  },
  wanAxis: (wan: number) => grouped(Math.round(wan * 10) / 10),
  number: (n: number) => grouped(n),
  count: (n: number) => `${grouped(n)} 次`,
  items: (n: number) => `${grouped(n)} 个`,
  percent: (rate: number) => `${oneDecimal(rate * 100)}%`,
  wholePercent: (rate: number) => `${Math.round(rate * 100)}%`,
  usd: (usd: number) => `$${usd < 10 ? usd.toFixed(2) : grouped(Math.round(usd))}`,
  // 等待与间隔：不到 1 分钟写秒，不到 1 小时写分（一位小数），再长写小时。
  seconds: (s: number) => (s < 60 ? `${grouped(Math.round(s))} 秒` : s < 3600 ? `${oneDecimal(s / 60)} 分` : `${oneDecimal(s / 3600)} 小时`),
  // 回合时长：1 分 40 秒这样的写法。
  clock: (s: number) => {
    const total = Math.round(s)
    if (total < 60) return `${total} 秒`
    if (total < 3600) return total % 60 ? `${Math.floor(total / 60)} 分 ${total % 60} 秒` : `${total / 60} 分`
    const minutes = Math.round((total % 3600) / 60)
    return minutes ? `${Math.floor(total / 3600)} 小时 ${minutes} 分` : `${Math.floor(total / 3600)} 小时`
  },
  // 你多久回、等了多久：分钟取整，两小时以上写小时。
  minutes: (s: number) => (s < 60 ? '不到 1 分钟' : s < 7200 ? `${Math.round(s / 60)} 分钟` : `${oneDecimal(s / 3600)} 小时`),

  kpis: {
    label: '关键指标',
    unrequested: '无人要求的消耗',
    unrequestedDelta: (wakes: number, tokens: string) => `定时唤醒 ${grouped(wakes)} 次，${tokens}`,
    context: '每次回复平均上下文',
    contextDelta: (compactions: number, top: { name: string; compactions: number } | null) =>
      top ? `压缩 ${grouped(compactions)} 次，${top.name} 占 ${grouped(top.compactions)}` : `压缩 ${grouped(compactions)} 次`,
    wait: '发出去多久开始回 p50',
    waitDelta: (p90: string) => `p90 ${p90}`,
    failures: '失败 + 中断',
    failuresDelta: (failed: number, interrupted: number) => `失败 ${grouped(failed)}，中断 ${grouped(interrupted)}`,
    questions: '等你回答',
    questionsDelta: (avg: string | null, pending: number) =>
      [avg && `你平均 ${avg}回`, pending ? `现在 ${pending} 个在等` : '现在没有在等的'].filter(Boolean).join('，'),
    tokens: 'token 合计',
    tokensDelta: (cost: string | null, hit: string | null) =>
      [cost && `估算 ${cost}`, hit && `缓存命中 ${hit}`].filter(Boolean).join('，'),
  },

  origins: {
    title: (hourly: boolean) => (hourly ? '每小时用量，按谁触发' : '每天用量，按谁触发'),
    unit: 'token（万）',
    console: '你发的消息',
    sleepWake: '定时唤醒',
    agents: 'Agent 互相提醒 / 任务',
    other: '其他',
    label: (share: string | null) => `每个时段的 token 用量按触发来源的堆叠柱状图${share ? `，定时唤醒占 ${share}` : ''}`,
  },

  wakes: {
    title: '谁在睡醒后烧钱',
    summary: (count: number, tokens: string) => `定时唤醒 ${grouped(count)} 次，共 ${tokens}`,
    once: '只醒了 1 次',
    every: (interval: string) => `每 ${interval}醒一次`,
    everyMinutes: (n: number) => `${n} 分钟`,
    everyHours: (n: number) => `${n} 小时`,
    everyDays: (n: number) => `${n} 天`,
    row: (cadence: string, avg: string) => `${cadence}，每次 ${avg}`,
    note: (name: string, avg: string, times: number | null) =>
      times ? `${name} 每次醒来平均 ${avg}，是其他 Agent 的 ${times} 倍。` : `${name} 每次醒来平均 ${avg}。`,
    prompt: '去看它的提示词',
    none: '这段时间没有定时唤醒。',
  },

  context: {
    title: '每次回复吃多少上下文',
    unit: '每次模型调用送进去的输入，含缓存读（万 token）',
    compactions: (n: number) => `压缩 ${grouped(n)} 次`,
    reopen: '该重开',
    reopenTitle: '平均上下文超过 12 万 token',
    note: '超过 12 万就该考虑让它重开会话；压缩次数越多，说明它越常把早先的东西忘掉。',
    none: '这段时间没有模型调用。',
  },

  questions: {
    title: '等你回答',
    unit: 'Agent 问你的问题',
    asked: (range: string) => `${range}提问`,
    avg: '你平均多久回',
    max: '最久等了',
    pending: '现在还在等的',
    waited: (time: string) => `等了 ${time}`,
    noPending: '现在没有在等你回答的问题。',
  },

  wait: {
    title: '发出去多久开始回',
    p50: '一半在此之内（p50）',
    p90: '九成（p90）',
    p50Short: 'p50',
    p90Short: 'p90',
    note: 'p90 高通常是排队：它正在回别的，你的消息在等。',
    label: '从你发消息到 Agent 开始回复的等待时长，p50 与 p90 折线',
    none: '这段时间你没有发消息。',
  },

  outcomes: {
    title: '回复结果',
    unit: (hourly: boolean) => (hourly ? '每小时回合数' : '每天回合数'),
    completed: '完成',
    interrupted: '被打断 / 停止',
    failed: '失败',
    note: (range: string, runs: number, rate: string | null) => `${range}共 ${grouped(runs)} 回合${rate ? `，完成 ${rate}` : ''}。`,
    label: (rate: string | null) => `每个时段的回复结果堆叠柱状图${rate ? `，完成率 ${rate}` : ''}`,
  },

  reasons: {
    title: '没完成的原因',
    total: (n: number) => `共 ${grouped(n)} 次`,
    kinds: { interrupted: '打断', stopped: '停止', failed: '失败' },
    // 结束原因与错误码的说法；不认识的直接显示代码。
    codes: {
      user_interrupt: '你发了打断消息',
      user_stopped: '你按了停止',
      server_restart: '服务重启，回复中断',
      abort_timeout: '打断超时被强制结束',
      credential_unavailable: '凭证不可用',
      cli_install_failed: 'CLI 安装失败',
      workspace_sync_failed: '工作区同步失败',
      session_missing: '会话文件丢失',
      pi_start_failed: 'Pi 启动失败',
      pi_exited: 'Pi 进程异常退出',
      pi_error: '模型服务报错，重试后仍失败',
      internal_error: '服务端内部错误',
      plugin_blocked: '插件阻止了这一轮',
      image_unsupported: '模型不支持图片',
    } as Record<string, string>,
    noCode: '没有记录原因',
    note: (failed: number, n: number, text: string) => `失败 ${grouped(failed)} 次里有 ${grouped(n)} 次是${text}。`,
    none: '这段时间的回复都完成了。',
  },

  more: '更多 · 排查时再看',

  health: {
    title: 'Agent 体检',
    label: '每个 Agent 的体检指标',
    columns: {
      name: 'Agent',
      runs: '回复',
      completion: '完成率',
      waitP90: '等待 p90',
      durationP50: '回合时长 p50',
      wakes: '唤醒',
      wakeTokens: '唤醒消耗',
      context: '平均上下文',
      compactions: '压缩',
      tokens: 'token',
      cache: '缓存命中',
    },
    none: '这段时间没有 Agent 在工作。',
  },

  tools: {
    title: '工具调用',
    unit: '次数 · 失败率',
    failureRate: (name: string, rate: string) => `${name} 失败率 ${rate}`,
    note: (avg: number, max: number) => `平均每回合 ${oneDecimal(avg)} 次调用，最多的一回合 ${grouped(max)} 次。`,
    none: '这段时间没有工具调用。',
  },

  process: {
    title: '模型与进程',
    retries: '模型重试',
    piExits: 'Pi 异常退出',
    piExitsNote: '含打断超时被强制停止',
    coldStarts: 'Pi 冷启动',
    coldStartAvg: (seconds: string) => `平均 ${seconds}`,
    coldStartSeconds: (ms: number) => `${oneDecimal(ms / 1000)} 秒`,
    byModel: 'token 按模型',
    share: (family: string, share: string) => `${family} ${share}`,
    others: (shares: string[]) => shares.join('，'),
    families: { claude: 'Claude', gpt: 'GPT', other: '其他' },
    cost: '估算花费',
    costNote: '按 API 单价折算，仅供参考',
    unpriced: (models: string[]) => `未计入没有单价的模型：${models.map((m) => m || '未知模型').join('、')}`,
    noPrice: '没有可估算的模型',
  },

  busy: {
    title: '一天里有多忙',
    unit: '同时在回复的 Agent 数',
    series: '平均同时回复',
    peak: (hour: number, value: string) => `${hour} 点 · ${value}`,
    value: (value: number) => oneDecimal(value),
    label: (peak: string | null) => `按小时的平均并发回复数${peak ? `，${peak} 最高` : ''}`,
  },
} as const
