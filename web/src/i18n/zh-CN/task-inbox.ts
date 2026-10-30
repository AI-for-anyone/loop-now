export const taskInbox = {
  toolsUnsupported: '当前服务尚未支持任务运行记录，未改用常态 Agent 记录。',
  scopeMismatch: 'Inbox 或运行记录返回的原任务、执行、Agent 或实例不匹配，未使用该响应。',
  readOriginal: '读取原发送结果', reading: '正在读取原动作…', notFound: '尚未找到原动作；结果仍待确认，不自动换动作或重新发送。',
  accepted: 'Inbox 已接收；读取、实际执行和回复以原记录为准。',
  links: '实际轮次关联', notification: '提醒轮次（不表示已读）', readLink: '实际读取轮次', replyLink: '实际回复轮次',
  notExecuted: '原节点消息未执行', timelinePartial: '运行记录尚未加载完整；当前仅显示已加载部分。',
  timelineMore: '加载后续运行记录', workMode: '节点工作', chatMode: '任务内排查对话', unknownMode: '历史执行模式未记录',
  recordsLoading: '正在读取任务工具与运行记录…', message: 'Inbox 消息', reply: 'Inbox 回复', workOutput: 'Agent 工作输出',
  inReplyTo: '回复原消息', original: '原 Inbox 记录', unread: '已接收，Agent 未读', read: 'Agent 已读取',
} as const
