import type { components } from '@/api/schema.gen'
import { formatDayClock, formatBytes, formatMonthDayClock } from './format'

type S = components['schemas']
// The failures of a runtime's CLI (runtime_*), worded with the runtime's name.
type RuntimeErrorCode = Extract<S['RunErrorCode'], `runtime_${string}`>
// A runtime's (Latin) name set off by spaces inside a Chinese sentence; "运行时" without spaces when the runtime isn't known. At the start
// of a sentence there is no space before the name.
const spaced = (name: string | null, inside = true) => (name ? `${inside ? ' ' : ''}${name} ` : '运行时')

export const chat = {
  loadingOlder: '正在加载更早的消息…',
  noMessages: '还没有对话，发一条消息开始吧。',
  inputPlaceholder: (name: string) => `给 ${name} 发消息`,
  busyPlaceholder: (name: string) => `${name} 正在回复，新消息会排队`,
  send: '发送',
  queueSend: '排队发送',
  queueSendTitle: '排队发送（Enter）',
  interruptSend: '打断并发送',
  emptyMessage: '消息不能为空',
  tooLong: (limit: number) => `消息超过上限（${formatBytes(limit)}）`,
  busyBar: (phase: string) => `Agent 正忙（${phase}）`,
  inputLabel: '消息',
  // Pi command menu that pops up when the input starts with "/"; only /pi:<name>
  // is handed to Pi to run, and other "/" messages are sent as usual.

  // Extension command handled directly by Pi without going to the model: this turn has no assistant reply.
  piCommandHandled: '命令已执行，没有回复',
  // Extension command output (notify messages) went over the limit and was truncated; the warning or error level is shown above the block.
  piCommandTruncated: '输出过长，已截断',
  noticeLevel: { warning: '警告', error: '错误' },
  you: '你',
  agent: 'Agent',
  queuedTag: '排队中',
  interruptTag: '打断',
  status: {
    waiting_slot: '等待执行名额',
    preparing: '准备运行环境',
    launching: '启动中',
    starting: '启动中',
    running: '回复中',
    aborting: '正在打断',
    failed: '失败',
    user_interrupt: '已打断',
    user_stopped: '已停止',
    server_restart: '服务端重启中断',
    user_cancelled: '排队中被撤回，没有执行',
    merged_into_interrupt: '消息已转为打断，这一轮没有执行',
    no_pending_messages: '消息已被读取或取消，这一轮没有执行',
    sleep_superseded: 'sleep 到期安排已由新工作承接',
    sleep_invalidated: 'sleep 到期安排已失效',
  },
  failure: (reason: string, message: string | null) => (message ? `失败：${reason}（${message}）` : `失败：${reason}`),
  runError: {
    credential_unavailable: '凭证不可用',
    cli_install_failed: 'CLI 安装失败',
    workspace_sync_failed: '工作区同步失败',
    session_missing: '会话找不到了',
    pi_start_failed: 'Agent 启动失败',
    pi_exited: 'Agent 进程异常退出',
    pi_error: 'Agent 报告错误',
    plugin_blocked: '插件阻止了本轮执行',
    image_unsupported: '当前模型不支持图片输入',
    internal_error: '服务端内部错误',
  } satisfies Record<Exclude<S['RunErrorCode'], RuntimeErrorCode>, string>,
  // A runtime's failures name the runtime that failed (its name, or "运行时" when it isn't known); a login that has expired reads the same
  // as one that never happened.
  runtimeError: {
    runtime_unavailable: (name: string | null) => `本机的${spaced(name)}用不了：没找到它、读不出版本，或版本太旧`,
    runtime_login_required: (name: string | null) => `本机的${spaced(name)}没有登录，或登录已失效：在本机终端重新登录后再试`,
    runtime_start_failed: (name: string | null) => `${spaced(name, false)}没能启动`,
    runtime_exited: (name: string | null) => `${spaced(name, false)}进程在这一轮中意外退出`,
    runtime_error: (name: string | null) => `${spaced(name, false)}报告这一轮出错`,
  } satisfies Record<RuntimeErrorCode, (name: string | null) => string>,
  tool: {
    started: '进行中',
    succeeded: '成功',
    failed: '失败',
  } satisfies Record<S['ToolState'], string>,
  sessionDivider: '会话已切换，页面历史保留',
  // Where the conversation was handed to another runtime (Run.contextHandoff), and where the model changed on the same runtime.
  handoff: {
    route: (from: string, to: string) => `${from} → ${to}`,
    label: (from: string, to: string, parts: string) => `对话从 ${from} 交给了 ${to}：${parts}`,
    // The change was undone after the other runtime had run a message: the same runtime starts a new session with the conversation.
    renewed: (runtime: string) => `${runtime} · 新会话`,
    renewedLabel: (runtime: string, parts: string) => `对话交给了 ${runtime} 的新会话：${parts}`,
    // The runtime's session could not be found, so a new one started with the saved conversation instead of stopping (reason
    // session_lost).
    lost: '原来的会话找不到了，已带着之前的对话重新开始',
    lostLabel: (runtime: string, parts: string) => `${runtime} 原来的会话找不到了，已带着之前的对话开了新会话：${parts}`,
    // A task instance on a runtime that can't fork the Agent's session starts with a selection of the regular conversation (task_start).
    task: '任务开始时带上了普通对话的摘选',
    taskLabel: (runtime: string, parts: string) => `任务在 ${runtime} 上开始，带上了普通对话的摘选：${parts}`,
    whole: (count: number) => `之前的 ${count} 条全部带过去`,
    included: (count: number) => `带过去 ${count} 条`,
    omitted: (count: number) => `省略 ${count} 条`,
    images: (count: number) => `${count} 张图片没带`,
    separator: '，',
  },
  modelChange: (from: string, to: string) => `模型从 ${from} 换成了 ${to}`,
  commands: {
    menuLabel: '命令',
    loading: '正在读取命令…',
    failed: (reason: string) => `读不到命令列表：${reason}`,
    notCaptured: 'Agent 运行一次后才有命令列表。',
    noMatch: '没有匹配的命令。',
    noPi: (runtime: string) => `没有匹配的命令。这个 Agent 用 ${runtime} 运行，没有 Pi 命令。`,
    // Grayed-out commands below the menu: unavailable right now, each with its reason.
    unavailable: {
      title: '现在不能用',
      onlyBlocked: '匹配的命令现在都不能用，回车会照原文发出',
      runtimeUnsupported: (runtime: string) => `这个 Agent 用 ${runtime} 运行，不支持这条命令`,
      reasons: { runtime_unsupported: '这个 Agent 的运行时不支持这条命令', session_none: '还没有会话，先发一条消息', session_missing: '会话找不到了，先 /new 或 /resume' },
    },
    source: { extension: '扩展命令', prompt: '提示模板', skill: '技能' } as Record<string, string>,
    plugin: (name: string) => `插件 · ${name}`,
    keys: '↑↓ 选择 · Enter 或 Tab 补全 · Esc 关闭',
    platform: '平台命令',
    noReply: '命令已执行',
    session: (session: S['ConsoleSessionStatus']) => `会话 ${{ none: '无会话', active: '活动', missing: '丢失' }[session.status.session.state]} · epoch ${session.status.session.epoch} · 模型 ${session.model.provider}/${session.model.modelId} · 思考 ${session.thinkingLevel} · 进程 ${{ stopped: '已停止', starting: '启动中', idle: '空闲', busy: '忙碌' }[session.status.processState]} · 排队 ${session.status.queued.normal + session.status.queued.interrupt}`,
    compact: (before: number, after: number) => `上下文已压缩：${before} → 约 ${after} tokens`,
    // Compacted, on a runtime that doesn't report the tokens before and after (Codex).
    compacted: '上下文已压缩',
    // Cases where Pi didn't compact but finished normally.
    compactSkipped: {
      too_small: '会话内容太少，不用压缩',
      already_compacted: '上次压缩之后还没有新内容，不用再压缩',
      cancelled_by_extension: '这次压缩被扩展取消了',
    } satisfies Record<NonNullable<S['RunCompaction']['skipReason']>, string>,
    reloaded: '已停止旧进程，下一条消息将重新加载',
    reloadNotNeeded: '没有运行中的进程，无需重载',
    newSession: (epoch: number) => `已重开会话（epoch ${epoch}），页面历史仍保留`,
    copied: '已复制最近一条成功回复',
    nothingToCopy: '没有可复制的回复',
    // When the /copy content couldn't be written to the clipboard, the result line explains how to copy it by hand.
    copyManual: '浏览器没有允许控制台写入剪贴板。内容在下面：点“复制”再试一次，或者选中后自己复制。',
    copyManualLabel: '要复制的内容',
    exported: (format: string) => `已下载 ${format} 会话导出文件`,
    newConfirmTitle: '确认重开会话？',
    newConfirmBody: '重开后 Agent 不再记得之前的对话内容，这个操作不能撤销；页面上的历史记录仍然保留。',
    newConfirmAction: '重开会话',
    noSessionsToResume: '没有可恢复的会话',
    resumeList: '可恢复的会话',
    resumeAction: '恢复',
    noTextSession: '无文字消息',
    currentSession: '当前',
    resumedSession: (epoch: number) => `已恢复会话（epoch ${epoch}），页面历史仍保留`,
    resumeConfirmTitle: '确认恢复这个会话？',
    resumeConfirmBody: '只恢复模型对话上下文，页面历史保留；不会回滚工作区、模型配置、Prompt、Skills 或权限。',
    resumeConfirmAction: '恢复会话',
  },  // Jumping to a manual compaction (/compact): it isn't a turn, so a note is shown where it happened.
  compactionNote: {
    title: '手动压缩',
    target: '定位到的手动压缩',
    notATurn: '手动压缩不是对话里的一轮，没有消息；它发生在这两轮之间。',
    failed: '压缩失败',
    interrupted: '压缩被取消或中断，没有完成',
    running: '正在压缩…',
  },

  // The turn woken by the Inbox: the sender is shown as a platform reminder.
  platformReminderAt: (time: string) => `平台提醒 · ${formatDayClock(time)}`,
  inboxReminder: { title: '平台提醒', from: (name: string) => `来自 ${name}`, deleted: (label: string) => `（${label}）`, received: '收到 Inbox 消息' },
  connection: {
    connecting: '正在连接…',
    open: '实时连接正常',
    reconnecting: '连接中断，正在重连…',
  },
  copyCode: '复制代码',
  mermaid: { diagram: 'Mermaid 图表', streaming: '图表尚在生成，等待代码块完成', rendering: '正在绘制图表…', failed: '图表未能绘制，已保留源码' },
  footnoteLabel: '脚注',
  footnoteBackLabel: (reference: number, rereference: number) =>
    rereference > 1 ? `返回引用 ${reference}-${rereference}` : `返回引用 ${reference}`,
  toolsLabel: '工具调用',
  messagesLabel: '对话',
  loadOlder: '加载更早的消息',
  // Ruler in the stub on the right of the sheet: one tick per conversation turn, named after the opening text of that turn.
  ruler: {
    label: '对话轮次',
    // Small heading at the top of the stub and the total number of turns at the bottom.
    title: '轮次',
    total: (count: number) => `共 ${count} 轮`,
    // Turns from a platform reminder or a sleep wake-up: say which kind of reminder, then the text.
    labeled: (kind: string, text: string) => `${kind}：${text}`,
    noText: '没有文字的一轮',
  },
  // Time above messages and replies, and the separator between name and time.
  time: (time: string) => formatMonthDayClock(time),
  metaSeparator: '·',
  // With reduced motion, the spinning icon of a tool in progress is replaced by this static mark.
  toolStaticMark: '…',
  // Tools that hadn't finished in an interrupted turn.
  toolInterrupted: '已打断',
  interruptDivider: '已打断，先处理这条',
  // Key hints under the input: Enter queues, ⌘/Ctrl+Enter sends as an interrupt.
  keyHint: { enter: 'Enter', modEnter: (mac: boolean) => (mac ? '⌘ Enter' : 'Ctrl Enter'), queue: '排队', interrupt: '立即打断', stopMark: '■', stop: '只停止当前回复' },
  // How keys are written in the chat page's input area: the Chinese UI always writes “回车” (Enter); modifier
  // keys are written as before. Key hints in the task node input still use the keyHint wording above.
  barKeys: { enter: '回车', shiftEnter: 'Shift + 回车', modEnter: (mac: boolean) => (mac ? '⌘ 回车' : 'Ctrl 回车') },
  // Key hints under the input when idle: “回车 发送 · Shift + 回车 换行” (Enter send · Shift + Enter new line).
  idleHint: { send: '发送', newline: '换行' },
  // When busy with an empty input, the send button becomes Stop: it stops only this
  // reply and sends no new message; messages queued after it are handled as usual.
  stop: '停止当前回复',
  stopping: '正在停止当前回复…',
  retryStop: '重试停止当前回复',
  queueList: {
    region: '排队消息',
    title: (count: number) => `排队 ${count}`,
    hint: '当前回复结束后按顺序处理',
    next: '下一条处理',
    promote: '打断',
    promoting: '打断中…',
    retryPromote: '重试打断',
    promoteTitle: '转为打断，下一条处理',
    edit: '编辑这条排队消息',
    editTitle: '编辑：撤回这条消息，放回输入框',
    cancel: '取消这条排队消息',
    retryCancel: '重试取消这条排队消息',
    cancelTitle: '取消',
    dispatched: '已开始处理，无法转为打断',
    dispatchedCancel: '已开始处理，无法取消',
    read: '这条消息已被读取，不在排队中了',
    cancelled: '这条消息已被取消',
    notExecuted: '这条消息已失效，无法转为打断',
    stopEnded: '这次回复已经结束',
    receiptMismatch: '服务端返回的结果与这条排队消息不一致，请刷新后核对',
    expand: '展开',
    collapse: '收起',
    imagesOnly: (count: number) => `${count} 张图片`,
  },
} as const
