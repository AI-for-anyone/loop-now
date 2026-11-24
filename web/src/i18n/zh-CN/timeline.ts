import type { components } from '@/api/schema.gen'
import { formatMonthDayClock, formatPreciseClock, formatSlept, formatStepDuration } from './format'

type S = components['schemas']

export const timeline = {
  toggle: (duration: string, tokens: string) => `运行记录 · ${duration} · ${tokens} token`,
  // A turn whose runtime reported no usage (Run.usageReported false): its zeros mean "not known".
  toggleNoUsage: (duration: string) => `运行记录 · ${duration} · 没有用量数据`,
  toggleRunning: (elapsed: string) => `运行记录 · 已用 ${elapsed}`,
  // The toggle bar is drawn as a segmented ticket: these are the segments after the name.
  toggleParts: { tokens: (tokens: string) => `${tokens} token`, noUsage: '没有用量数据', running: (elapsed: string) => `已用 ${elapsed}` },
  title: '运行记录',
  total: (duration: string) => `合计 ${duration}`,
  loading: '正在加载运行记录…',
  meta: {
    model: (provider: string, model: string) => `model=${provider} / ${model}`,
    noModel: '没有模型调用',
    config: (version: number) => `配置 v${version}`,
    whenBusy: {
      idle: '空闲时发送',
      queue: 'whenBusy=queue（排队）',
      interrupt: 'whenBusy=interrupt（打断）',
    },
  },
  step: {
    queued: '入队',
    dispatched: '派发',
    waiting_slot: '等待执行名额',
    preparing: '准备运行环境',
    // The process steps name the runtime (Pi or Claude Code, taken from the entry; old records lack it, which means Pi).
    launching: (runtime: string) => `${runtime} 启动`,
    cli_install: (cli: string) => `安装 ${cli}`,
    workspace_sync: '同步配置与文件夹',
    process_started: (runtime: string) => `${runtime} 启动`,
    process_stopped: (runtime: string) => `${runtime} 进程停止`,
    process_exited: (runtime: string) => `${runtime} 进程意外退出`,
    // A handover, by why it happened (ContextHandoff.reason).
    context_handoff: {
      runtime_changed: '对话已交接',
      session_lost: '会话找不到了，已带着对话重开',
      task_start: '开始时带上了普通对话的摘选',
    },
    running: '开始回复',
    tool: (name: string) => `工具 ${name}`,
    model_call: {
      assistant: '模型调用',
      tool: '工具自身的用量',
      compaction: '上下文压缩的用量',
      cache_warm: '缓存预热',
      other: '其他用量',
    },
    model_retry_started: '模型自动重试',
    model_retry_ended: '自动重试结束',
    compaction: '上下文压缩',
    abort_requested: '收到打断消息',
    stop_requested: '被人停止',
    promoted: { interrupt: '已转为打断', queue: '已转为排队' } satisfies Record<S['WhenBusy'], string>,
    message_cancelled: '一条 Inbox 消息被取消',
    session_missing: '会话找不到了',
    model_rate_limit: '订阅额度',
    // The reply had ended but commands it runs in the background were still going; the turn waited, and the runtime sent nothing for
    // limitMs, so the turn ended there (tools still open were closed as no_result).
    background_wait_timeout: (limitMs: number) => `等后台命令时运行时 ${formatSlept(limitMs)}没有任何消息，这一轮在这里结束`,
    ended: {
      completed: '完成',
      failed: '失败',
      user_interrupt: '已打断',
      server_restart: '服务端重启中断',
    },
  },
  detail: {
    idleDispatch: 'Agent 空闲，直接派发。',
    queued: { queue: 'Agent 正忙，排在普通排队消息末尾。', interrupt: 'Agent 正忙，打断当前回复，排在普通排队消息之前。' },
    installOk: (version: string) => `已安装 ${version}。`,
    installFailed: (version: string, error: string | null) => (error ? `安装 ${version} 失败：${error}` : `安装 ${version} 失败。`),
    syncOk: (count: number) => (count > 0 ? `已同步 ${count} 个文件夹来源。` : '配置已同步。'),
    syncFailed: (error: string | null) => (error ? `同步失败：${error}` : '同步失败。'),
    syncSource: {
      cloned: (path: string) => `${path}：已克隆`,
      kept: (path: string) => `${path}：仓库已存在，保持不动`,
      files_synced: (path: string, written: number, deleted: number) => `${path}：资料已同步（写入 ${written} 个，删除 ${deleted} 个）`,
      failed: (path: string, error: string | null) => (error ? `${path}：失败（${error}）` : `${path}：失败`),
    },
    processStarted: (runtime: string, pid: number, reason: string) => `启动新的 ${runtime} 进程，PID ${pid}（${reason}）。`,
    piStartReason: { no_process: '没有存活的进程', config_changed: '配置修改后重启' } as Record<string, string>,
    processReused: (runtime: string, pid: number) => `复用常驻的 ${runtime} 进程，PID ${pid}。`,
    processStopped: (pid: number, reason: string, uptime: string) => `PID ${pid}，${reason}，运行了 ${uptime}。`,
    processExited: (pid: number, exit: string, uptime: string) => `PID ${pid}，${exit}，运行了 ${uptime}。`,
    // The selection of the saved conversation given to a new runtime: route, what went along, and its size against the budget (one token
    // per byte).
    handoff: (route: string, included: number, omitted: number, images: number, used: string, budget: string) =>
      [route, [`带过去 ${included} 条`, omitted > 0 ? `省略 ${omitted} 条` : null, images > 0 ? `${images} 张图片没带` : null].filter(Boolean).join('，'), `预算 ${budget}，用了 ${used}`].join(' · '),
    // Subscription limits reported by Claude Code (info is passed through from claude as is, so
    // fields may be missing): limit type, status, reset time; unknown values are shown as is.
    rateLimit: {
      type: { five_hour: '5 小时额度', seven_day: '7 天额度', seven_day_opus: '7 天 Opus 额度', seven_day_sonnet: '7 天 Sonnet 额度', overage: '超额用量' } as Record<string, string>,
      status: { allowed: '可用', allowed_warning: '快用完了', rejected: '已用完' } as Record<string, string>,
      resetsAt: (time: string) => `${formatMonthDayClock(time)} 重置`,
      empty: '没有给出额度详情',
    },
    modelCall: (model: string, input: string, output: string, cacheRead: string, cacheWrite: string) =>
      `${model} · 输入 ${input} · 输出 ${output} · 缓存读取 ${cacheRead} · 缓存写入 ${cacheWrite}`,
    retryStarted: (attempt: number, max: number, delay: string, error: string) => `第 ${attempt}/${max} 次，${delay} 后重试：${error}`,
    retryEnded: (success: boolean, attempt: number, error: string | null) =>
      success ? `第 ${attempt} 次重试成功。` : error ? `重试 ${attempt} 次后仍然失败：${error}` : `重试 ${attempt} 次后仍然失败。`,
    compaction: {
      succeeded: (before: string | null, after: string | null) =>
        before && after ? `压缩完成：约 ${before} → ${after} token。` : '压缩完成。',
      failed: (error: string | null) => (error ? `压缩失败：${error}` : '压缩失败。'),
      aborted: '压缩被中止。',
    },
    toolRunning: '进行中',
    // A tool closed as no_result, or one still on started in a turn that has ended (records saved before no_result existed).
    toolNoResult: '没有结果',
    commandUnavailable: '未记录命令',
    // The session's reference: a file for Pi and Claude Code, a thread for Codex.
    sessionFile: (file: string) => `会话：${file}`,
    completed: '回复正常结束。',
  },
  field: {
    queued: 'queuedAt',
    dispatched: 'startedAt · status=starting',
    phase: (phase: string) => `phase=${phase}`,
    running: 'runningAt · status=running',
    tool: (state: string) => (state === 'succeeded' ? null : `state=${state}`),
    abort: 'abortRequestedAt · status=aborting',
    stop: 'abortRequestedAt · stop=true',
    promoted: (from: string, to: string) => `whenBusy=${from} → ${to}`,
    message: (messageId: string) => `messageId=${messageId}`,
    ended: (status: string) => `endedAt · status=${status}`,
  },
  usage: {
    title: 'token 用量',
    field: 'usage',
    calls: (count: number) => `模型调用 ${count} 次`,
    unreported: '这一轮运行时没有报告 token 用量。',
  },
  result: {
    completed: '完成',
    failed: '失败',
    interrupted: '被打断',
    stopped: '已停止',
    voided: '未执行',
    server_restart: '服务端重启中断',
    errorField: (code: string, message: string | null) => (message ? `errorCode=${code} · errorMessage="${message}"` : `errorCode=${code}`),
    endReasonField: (reason: string) => `endReason=${reason}`,
    statusField: (status: string) => `status=${status}`,
  },
  // Duration: milliseconds under 1 second, one decimal place under 100 seconds, minutes and seconds beyond that.
  duration: (ms: number) => formatStepDuration(ms),
  clock: (time: string) => formatPreciseClock(time),
  locate: {
    viewing: '正在查看定位的回复',
    newMessages: '有新消息',
    backToLatest: '回到最新',
    loadNewer: '加载更新的消息',
    loadingNewer: '正在加载更新的消息…',
    notFound: '找不到这条回复，下面是最新的对话。',
    target: '定位到的回复',
  },
} as const
