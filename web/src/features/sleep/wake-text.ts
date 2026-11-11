// 到期唤醒消息是平台写给模型的指令，现在是英文：
// "Platform notice: your sleep is due; continue in the original conversation. sleepId=<ID>"，有说明时再接 "; note: <说明>"。
// 改成英文之前的会话里是中文："平台通知：sleep已到期，请接着原会话处理。sleepId=<ID>"，有说明时再接"；说明：<说明>"，不迁移，两种都认。
// 界面只取 Agent 自己写的说明；格式对不上时返回 null，原文只放进悬停提示。
const wakeTexts = [
  /^Platform notice: your sleep is due; continue in the original conversation\. sleepId=[0-9a-f-]+(?:; note: ([\s\S]*))?$/u,
  /^平台通知：sleep已到期，请接着原会话处理。sleepId=[0-9a-f-]+(?:；说明：([\s\S]*))?$/u,
]
export function sleepWakeNote(text: string): { note: string } | null {
  const trimmed = text.trim()
  for (const wakeText of wakeTexts) {
    const match = wakeText.exec(trimmed)
    if (match) return { note: match[1]?.replace(/\s+/gu, ' ').trim() ?? '' }
  }
  return null
}
