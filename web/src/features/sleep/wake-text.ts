// 到期唤醒消息是平台写给模型的指令：“平台通知：sleep已到期，请接着原会话处理。sleepId=<ID>”，有说明时再接“；说明：<说明>”。
// 界面只取 Agent 自己写的说明；格式对不上时返回 null，原文只放进悬停提示。
const wakeText = /^平台通知：sleep已到期，请接着原会话处理。sleepId=[0-9a-f-]+(?:；说明：([\s\S]*))?$/u
export function sleepWakeNote(text: string): { note: string } | null {
  const match = wakeText.exec(text.trim())
  return match ? { note: match[1]?.replace(/\s+/gu, ' ').trim() ?? '' } : null
}
