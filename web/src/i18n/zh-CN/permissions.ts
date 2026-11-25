// Permission modes (as T3): how often an Agent on a CLI asks before it acts, and the requests it sends to be approved.
export const permissions = {
  title: '批准模式',
  modes: {
    supervised: { name: '逐项批准', hint: '运行命令、改文件之前都先问你' },
    accept_edits: { name: '改文件不用问', hint: '改文件直接做，其他操作照样先问你' },
    auto: { name: '自动审查', hint: '由 CLI 自己的自动审查放行常规操作，拿不准的再问你' },
    full: { name: '完全放开', hint: '运行命令、改文件都不问你（以前一直是这样）' },
  },
  current: (mode: string) => `批准模式：${mode}`,
  // A runtime without the chosen mode runs it as Supervised, never as Full access.
  fallback: (runtime: string, mode: string) => `${runtime} 没有「${mode}」，会按「逐项批准」运行`,
  onlyFull: (runtime: string) => `${runtime} 不会请求批准，只能完全放开`,
  nextMessage: '从下一条消息开始生效',
  saving: '正在保存…',
  saved: '已保存',
  // Settings: the default for new Agents.
  defaultTitle: '新 Agent 的批准模式',
  defaultHint: '只影响之后新建和导入的 Agent；已有的 Agent 在它们的配置页里改。',
  // The message box, at the bottom of the model picker.
  pickerTitle: '批准',
  // A request waiting for an answer, in the chat and the task node.
  request: {
    label: '等你批准',
    asks: (agent: string, kind: string) => `${agent} 想${kind}`,
    kinds: { command: '运行命令', file_change: '修改文件', tool: '使用工具', network: '访问网络', other: '做一件事' },
    approve: '批准',
    reject: '拒绝',
    approving: '正在批准…',
    rejecting: '正在拒绝…',
    reasonLabel: '拒绝理由',
    reasonPlaceholder: '写下理由会拒绝它，并把理由告诉它',
    rejectWithReason: '拒绝并说明',
    waiting: (count: number) => `${count} 个请求在等你批准，回复先停在这里`,
    alsoInInbox: '收件箱里也有这个问题，在哪边回答都行',
    answeredElsewhere: (outcome: string) => `已在别处回答：${outcome}`,
    states: { pending: '等待中', approved: '已批准', rejected: '已拒绝', cancelled: '已取消' },
    reason: (text: string) => `理由：${text}`,
    stopRejects: '停止回复会拒绝所有还在等的请求',
    show: '去看看',
    cancelReasons: { reply_stopped: '回复被停止', reply_ended: '回复结束了', process_exited: 'CLI 退出了' } as Record<string, string>,
  },
  // Inbox: a question of kind permission.
  inbox: {
    title: (agent: string) => `${agent} 请求批准`,
    open: '去对话里看',
    badge: '请求批准：回答就是批准或拒绝，写下理由会拒绝它',
  },
  desktop: {
    title: (agent: string) => `${agent} 请求批准`,
  },
} as const
