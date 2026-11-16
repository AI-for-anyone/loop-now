// Messages for the operator (@console) no longer have a separate inbox page: messages and
// questions an Agent sends you appear in its conversation, questions from task instances appear in
// the node conversation, and exchanges between an external identity and you appear on its page.
export const consoleInbox = {
  toYou: '发给你',
  unread: '未读',
  question: '问题',
  // The caller formats the time with t.inbox.time and passes it in.
  openUntil: (time: string) => `${time} 前有效`,
  chosen: (time: string, label: string) => `你在 ${time} 选了「${label}」`,
  answeredByText: (time: string, text: string) => `你在 ${time} 回复：${text}`,
  answeredBy: (name: string, time: string) => `${name} 在 ${time} 回答了`,
  replyToQuestion: '回复这个问题',
  cannotAnswer: '这个问题现在不能回答，请检查任务的最新状态。',
  locateQuestion: '在对话中回答',
  // Reply mode of the input: the text is sent as the answer to this question.
  replying: (name: string) => `回复 ${name} 的问题`,
  cancelReply: '取消回复这个问题',
  replyPlaceholder: '输入对这个问题的回答…',
  replyNoImages: '回复问题时不能附带图片，请先移除图片。',
  answered: '已回答这个问题。',
  // Exchanges with you on the external identity page.
  conversation: '与你的往来',
  conversationEmpty: '还没有和你的往来。',
  conversationIntro: '这个外部身份发给你的消息和你发给它的消息；回复默认接在最近一条消息之后。',
  newMessage: '新消息',
  newMessageLabel: (name: string) => `给 ${name} 发新消息`,
  newMessageHint: '新消息另起一条线索。',
  cancel: '取消',
  // “待你处理” (Needs you): the summary on the overview and the counts on the Agent and task lists.
  pendingTitle: '待你处理',
  pendingCount: (count: number) => `${count} 条`,
  pendingKind: { question: '问题', unread: '新消息' },
  inTask: (title: string) => `任务「${title}」`,
  taskFallback: '任务实例',
  // System notifications from the desktop shell.
  desktop: {
    question: (name: string) => `待你回复：${name}`,
    task: (title: string) => `任务等待：${title}`,
    inTask: (title: string, preview: string) => `任务「${title}」：${preview}`,
    from: (name: string, preview: string) => `${name}：${preview}`,
  },
  answer: '去回答',
  view: '查看',
  badge: (questions: number, unread: number) => [questions ? `${questions} 个问题` : '', unread ? `${unread} 条新消息` : ''].filter(Boolean).join('、'),
  badgeLabel: (summary: string) => `待你回复：${summary}`,
} as const
