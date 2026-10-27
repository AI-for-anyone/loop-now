import { randomBytes } from 'node:crypto'

// ULID（https://github.com/ulid/spec）：48 位毫秒时间加 80 位随机数，Crockford Base32，按字典序即按时间排序。
// 同一毫秒内递增随机部分，保证同一进程里生成的 id 严格递增。

const ALPHABET = '0123456789ABCDEFGHJKMNPQRSTVWXYZ'

let lastTime = -1
let lastRandom: number[] = []

function encodeTime(time: number): string {
  let out = ''
  for (let i = 0; i < 10; i++) {
    out = ALPHABET[time % 32] + out
    time = Math.floor(time / 32)
  }
  return out
}

function freshRandom(): number[] {
  const bytes = randomBytes(16)
  return Array.from({ length: 16 }, (_, i) => bytes[i]! % 32)
}

function increment(digits: number[]): number[] {
  const next = [...digits]
  for (let i = next.length - 1; i >= 0; i--) {
    if (next[i]! < 31) {
      next[i]!++
      return next
    }
    next[i] = 0
  }
  throw new Error('同一毫秒内生成的 ULID 超出上限')
}

export function ulid(now: number = Date.now()): string {
  if (now <= lastTime) {
    lastRandom = increment(lastRandom)
  } else {
    lastTime = now
    lastRandom = freshRandom()
  }
  return encodeTime(lastTime) + lastRandom.map((d) => ALPHABET[d]).join('')
}

export const ULID_PATTERN = /^[0-9A-HJKMNP-TV-Z]{26}$/
