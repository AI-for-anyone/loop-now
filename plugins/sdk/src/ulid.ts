import { randomBytes } from 'node:crypto'

// ULID (https://github.com/ulid/spec): 48-bit millisecond time plus 80 random bits, Crockford Base32; lexicographic order is time order.
// Within the same millisecond the random part is incremented, so ids generated in one process are strictly increasing.

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
  throw new Error('Too many ULIDs generated within the same millisecond')
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
