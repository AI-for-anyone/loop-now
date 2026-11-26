import { describe, expect, it } from 'vitest'
import {
  AVATAR_PAIRS,
  PROJECT_AVATAR_PALETTES,
  TRANSLUCENT_AVATAR_PAIRS,
  avatarPair,
  projectAvatarPalette,
  projectMonogram,
  translucentAvatarPair,
  translucentAvatarPalette,
} from './avatar'

describe('avatar color pairs', () => {
  it('the same Agent ID always gets the same color pair', () => {
    const id = '01a1334c-040e-73f3-8279-907bd401f2bf'
    expect(avatarPair(id)).toBe(avatarPair(id))
    expect(translucentAvatarPalette(id)).toBe(translucentAvatarPalette(id))
  })

  it('different IDs reach all four Plate color pairs', () => {
    const seen = new Set(Array.from({ length: 64 }, (_, i) => avatarPair(`agent-${i}`)))
    expect(seen.size).toBe(AVATAR_PAIRS.length)
  })

  it('different IDs reach all curated translucent pairs with alpha values in both light and dark themes', () => {
    const seen = new Set(Array.from({ length: 128 }, (_, i) => translucentAvatarPalette(`agent-${i}`).name))
    expect(seen.size).toBe(TRANSLUCENT_AVATAR_PAIRS.length)
    for (const pair of TRANSLUCENT_AVATAR_PAIRS) {
      for (const color of [...pair.light, ...pair.dark]) {
        expect(color).toMatch(/^rgba\(\d+, \d+, \d+, 0\.\d+\)$/)
      }
    }
    expect(translucentAvatarPair('agent-1', 'light')).not.toEqual(translucentAvatarPair('agent-1', 'dark'))
  })

  it('computes clean project monograms and distinct per-project palettes', () => {
    expect(projectMonogram('loop-now')).toBe('LN')
    expect(projectMonogram('docs_site')).toBe('DS')
    expect(projectMonogram('modelAccess')).toBe('MA')
    expect(projectMonogram('backend')).toBe('BA')
    const seen = new Set(Array.from({ length: 128 }, (_, i) => projectAvatarPalette(`project-${i}`).name))
    expect(seen.size).toBe(PROJECT_AVATAR_PALETTES.length)
  })
})
