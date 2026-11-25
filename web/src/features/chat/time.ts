import type { Schemas } from '@/api/client'

type Run = Schemas['Run']

// When the run started; for a run that hasn't started yet, when it was queued.
export function runStartedAt(run: Run): string {
  return run.startedAt ?? run.queuedAt
}

// A running clock for how long something has run: 0:07, 1:23, 1:02:03. Digits and colons read the same in every UI language.
export function elapsedClock(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  const hours = Math.floor(total / 3600), minutes = Math.floor((total % 3600) / 60), seconds = total % 60
  const two = (value: number) => String(value).padStart(2, '0')
  return hours > 0 ? `${hours}:${two(minutes)}:${two(seconds)}` : `${minutes}:${two(seconds)}`
}

// Compare two RFC 3339 times numerically. The server (Go) drops trailing zeros from fractional seconds, so the number of digits varies
// and comparing them as strings sorts them wrong; Date.parse is only precise to the millisecond, so the fractional part is padded to
// nanoseconds and compared separately.
// Times that can't be parsed count as equal, and the caller decides the order.
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
