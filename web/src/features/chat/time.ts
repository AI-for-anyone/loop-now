import type { Schemas } from '@/api/client'

type Run = Schemas['Run']

// 执行开始的时间；尚未开始的按入队时间。
export function runStartedAt(run: Run): string {
  return run.startedAt ?? run.queuedAt
}

// 按数值比较两个 RFC 3339 时间。服务端（Go）输出的小数秒去掉了末尾的 0，位数不定，
// 按字符串比较会排错；Date.parse 只精确到毫秒，所以小数部分补齐到纳秒后单独比较。
// 无法解析的时间视为相同，由调用方决定次序。
export function compareTimes(a: string, b: string): number {
  const ka = timeKey(a)
  const kb = timeKey(b)
  if (!ka || !kb) return 0
  return ka.seconds - kb.seconds || ka.nanos - kb.nanos
}

function timeKey(value: string): { seconds: number; nanos: number } | null {
  const match = /^(.+T\d{2}:\d{2}:\d{2})(?:\.(\d+))?(.*)$/.exec(value)
  if (!match) return null
  const [, head, fraction = '', zone] = match
  const seconds = Date.parse(head + zone)
  if (Number.isNaN(seconds)) return null
  return { seconds, nanos: Number(fraction.padEnd(9, '0').slice(0, 9)) }
}
