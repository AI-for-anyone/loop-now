// 操作者（@console）的消息不再有单独的收件箱页：Agent 发给你的消息与问题显示在它的对话里，
// 任务实例的问题显示在节点对话里，外部身份与你的往来显示在它的页面上。
export const consoleInbox = {
  toYou: '发给你',
  unread: '未读',
  question: '问题',
  // 时间由调用方用 t.inbox.time 格式化后传入。
  openUntil: (time: string) => `${time} 前有效`,
  chosen: (time: string, label: string) => `你在 ${time} 选了「${label}」`,
  answeredByText: (time: string, text: string) => `你在 ${time} 回复：${text}`,
  answeredBy: (name: string, time: string) => `${name} 在 ${time} 回答了`,
  replyToQuestion: '回复这个问题',
  cannotAnswer: '这个问题现在不能回答，请检查任务的最新状态。',
  locateQuestion: '在对话中回答',
  // 输入框的回复模式：文字作为这个问题的回答发出。
  replying: (name: string) => `回复 ${name} 的问题`,
  cancelReply: '取消回复这个问题',
  replyPlaceholder: '输入对这个问题的回答…',
  replyNoImages: '回复问题时不能附带图片，请先移除图片。',
  answered: '已回答这个问题。',
  // 外部身份页上与你的往来。
  conversation: '与你的往来',
  conversationEmpty: '还没有和你的往来。',
  conversationIntro: '这个外部身份发给你的消息和你发给它的消息；回复默认接在最近一条消息之后。',
  newMessage: '新消息',
  newMessageLabel: (name: string) => `给 ${name} 发新消息`,
  newMessageHint: '新消息另起一条线索。',
  cancel: '取消',
  // 待你处理：概览上的汇总与 Agent、任务列表上的数字。
  pendingTitle: '待你处理',
  pendingCount: (count: number) => `${count} 条`,
  pendingKind: { question: '问题', unread: '新消息' },
  inTask: (title: string) => `任务「${title}」`,
  taskFallback: '任务实例',
  answer: '去回答',
  view: '查看',
  badge: (questions: number, unread: number) => [questions ? `${questions} 个问题` : '', unread ? `${unread} 条新消息` : ''].filter(Boolean).join('、'),
  badgeLabel: (summary: string) => `待你回复：${summary}`,
} as const
