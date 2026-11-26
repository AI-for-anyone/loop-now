import type { components } from '@/api/schema.gen'

type S = components['schemas']
type Task = S['BackgroundTask']

// Background tasks: work a CLI started in the background that keeps running after its reply ended (a build, a test run, a dev
// server, a sub-agent).
export const background = {
  // The mark under the message box, and the panel it opens.
  mark: (count: number) => `后台任务 ${count}`,
  markLabel: (count: number) => `${count} 个后台任务在运行，点开查看或停止`,
  title: '后台任务',
  running: (count: number) => `${count} 个在运行`,
  hint: '回复已经结束，这些还在跑',
  nodeHint: '这一步要等它们结束才会完成；停掉其中一个不会停掉这一步',
  kind: { command: '命令', subagent: '子代理', other: '任务' } satisfies Record<Task['kind'], string>,
  // Which reply started it: its number when the conversation has it, otherwise its time.
  fromReply: (number: number) => `第 ${number} 轮回复`,
  fromReplyAt: (time: string) => `${time} 的回复`,
  fromReplyLabel: (reply: string) => `跳到${reply}`,
  elapsedLabel: (clock: string) => `已运行 ${clock}`,
  stop: '停止',
  stopLabel: (label: string) => `停止后台任务：${label}`,
  stopping: '正在停止…',
  retryStop: '再试一次',
  stopAll: (count: number) => `全部停止（${count}）`,
  stopAllHint: '停止后，开始它的回复里会记下“已停止”。',
  ended: '已经结束了',
  allEnded: '后台任务都结束了。',
  // A backgrounded tool in a reply, and how it ended.
  card: {
    label: '后台任务',
    chipLabel: (tool: string, status: string, label: string) => `${tool} ${status}：${label}`,
    status: { running: '后台运行中', completed: '后台已完成', failed: '后台出错', stopped: '后台已停止', unknown: '后台结果未知' } satisfies Record<Task['status'], string>,
    exitCode: (code: number) => `退出码 ${code}`,
    took: (duration: string) => `用时 ${duration}`,
    output: '输出',
    outputTail: '输出（最后一段）',
    stopReason: {
      person: '手动停止',
      reply_stopped: '随回复一起停止',
      settings_changed: '改了设置，重启时停止',
      runtime_switched: '换了运行方式，停止',
      session_reset: '重置会话时停止',
      agent_deleted: '删除 Agent 时停止',
      task_started: '从这个 Agent 开始任务时停止',
      shutdown: 'Loop Now 退出时停止',
      task_limit: '超过等待上限，自动停止',
    } satisfies Record<NonNullable<Task['stopReason']>, string>,
    unknownHint: 'CLI 意外退出或 Loop Now 重启过，不知道它最后怎样了',
  },
  // The heading of a reply the CLI wrote by itself after background work ended (no person's message above it).
  // The ended tasks follow as compact chips; when there are more than two, “+N” opens the rest.
  followUp: {
    heading: (runtime: string) => `后台任务结束后，${runtime} 接着说：`,
    unknown: (runtime: string) => `后台任务结束后，${runtime} 接着说：`,
    more: (count: number) => `+${count}`,
    moreLabel: (count: number) => `查看另外 ${count} 个已结束的后台任务`,
    moreTitle: (count: number) => `另外 ${count} 个后台任务`,
    ruler: '后台任务结束后',
  },
  // Lists (sidebar, overview, project Agents).
  listTag: (count: number) => `后台 ${count}`,
  listTitle: (count: number) => `空闲，${count} 个后台任务还在运行`,
  // Above the message box, and next to Stop.
  restartWarning: (runtime: string, count: number) => `设置已改：下一条消息会重启 ${runtime}，正在运行的 ${count} 个后台任务会被停掉。`,
  stopWarning: (count: number) => `停止或打断也会停掉 ${count} 个后台任务`,
  stopWithTasks: (count: number) => `停止（也会停掉 ${count} 个后台任务）`,
} as const
