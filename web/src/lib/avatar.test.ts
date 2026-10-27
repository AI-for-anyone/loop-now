import { describe, expect, it } from 'vitest'
import { AVATAR_PAIRS, avatarPair } from './avatar'

describe('头像色对', () => {
  it('同一个 Agent ID 总是得到同一组颜色', () => {
    const id = '01a1334c-040e-73f3-8279-907bd401f2bf'
    expect(avatarPair(id)).toBe(avatarPair(id))
  })

  it('不同的 ID 能取到全部四组色对', () => {
    const seen = new Set(Array.from({ length: 64 }, (_, i) => avatarPair(`agent-${i}`)))
    expect(seen.size).toBe(AVATAR_PAIRS.length)
  })
})
