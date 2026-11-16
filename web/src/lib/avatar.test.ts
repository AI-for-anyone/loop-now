import { describe, expect, it } from 'vitest'
import { AVATAR_PAIRS, avatarPair } from './avatar'

describe('avatar color pairs', () => {
  it('the same Agent ID always gets the same color pair', () => {
    const id = '01a1334c-040e-73f3-8279-907bd401f2bf'
    expect(avatarPair(id)).toBe(avatarPair(id))
  })

  it('different IDs reach all four color pairs', () => {
    const seen = new Set(Array.from({ length: 64 }, (_, i) => avatarPair(`agent-${i}`)))
    expect(seen.size).toBe(AVATAR_PAIRS.length)
  })
})
