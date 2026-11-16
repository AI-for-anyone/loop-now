// The wake-up message sent when a sleep is due is an instruction from the platform to the model, and is now in English:
// "Platform notice: your sleep is due; continue in the original conversation. sleepId=<ID>", followed by "; note: <note>"
// when there is a note.
// Sessions from before the switch to English have it in Chinese: "平台通知：sleep已到期，请接着原会话处理。sleepId=<ID>"
// (the same message in Chinese), followed by "；说明：<说明>" ("; note: <note>") when there is a note. They are not migrated;
// both forms are recognized.
// The UI only takes the note the Agent wrote itself; when the format doesn't match, return null, and the original text only
// goes into the hover tooltip.
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
