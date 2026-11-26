import { describe, expect, it, vi } from 'vitest'
import {
  SIDEBAR_WIDTH_DEFAULT,
  SIDEBAR_WIDTH_MAX,
  SIDEBAR_WIDTH_MIN,
  SIDEBAR_WIDTH_STEP,
  SIDEBAR_WIDTH_STORAGE_KEY,
  clampSidebarWidth,
  maxSidebarWidth,
  parseSidebarWidth,
  sidebarWidth,
} from './sidebar-width'

describe('sidebar width limits and clamping', () => {
  it('uses the default, min, max and keyboard step specified for the desktop sidebar', () => {
    expect(SIDEBAR_WIDTH_DEFAULT).toBe(248)
    expect(SIDEBAR_WIDTH_MIN).toBe(200)
    expect(SIDEBAR_WIDTH_MAX).toBe(420)
    expect(SIDEBAR_WIDTH_STEP).toBe(16)
  })

  it('caps the maximum width at 420px and 40% of the viewport, while never dropping below the minimum', () => {
    expect(maxSidebarWidth(1440)).toBe(420)
    expect(maxSidebarWidth(1050)).toBe(420)
    expect(maxSidebarWidth(1000)).toBe(400)
    expect(maxSidebarWidth(901)).toBe(360)
    expect(maxSidebarWidth(390)).toBe(200)
    expect(maxSidebarWidth(0)).toBe(420)
    expect(maxSidebarWidth(-100)).toBe(420)
    expect(maxSidebarWidth(Number.NaN)).toBe(420)
  })

  it('clamps widths into [min, max(viewport)], rounds fractions and falls back on non-finite values', () => {
    expect(clampSidebarWidth(300, 1440)).toBe(300)
    expect(clampSidebarWidth(150, 1440)).toBe(200)
    expect(clampSidebarWidth(500, 1440)).toBe(420)
    expect(clampSidebarWidth(400, 920)).toBe(368)
    expect(clampSidebarWidth(300.6, 1440)).toBe(301)
    expect(clampSidebarWidth(Number.NaN, 1440)).toBe(248)
    expect(clampSidebarWidth(Number.POSITIVE_INFINITY, 1440)).toBe(248)
  })

  it('parses stored strings safely, clamping valid numbers and falling back to the default on garbage', () => {
    expect(parseSidebarWidth(null)).toBe(248)
    expect(parseSidebarWidth('')).toBe(248)
    expect(parseSidebarWidth('   ')).toBe(248)
    expect(parseSidebarWidth('wide')).toBe(248)
    expect(parseSidebarWidth('NaN')).toBe(248)
    expect(parseSidebarWidth('Infinity')).toBe(248)
    expect(parseSidebarWidth('0')).toBe(248)
    expect(parseSidebarWidth('-40')).toBe(248)
    expect(parseSidebarWidth('120')).toBe(200)
    expect(parseSidebarWidth('600')).toBe(420)
    expect(parseSidebarWidth('312.6')).toBe(313)
    expect(parseSidebarWidth('320')).toBe(320)
  })
})

describe('sidebar width storage', () => {
  it('reads the default when nothing is stored, persists written values and clears back to the default', () => {
    expect(sidebarWidth.read()).toBe(248)

    sidebarWidth.write(336)
    expect(window.localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY)).toBe('336')
    expect(sidebarWidth.read()).toBe(336)

    sidebarWidth.write(999)
    expect(window.localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY)).toBe('420')

    sidebarWidth.clear()
    expect(window.localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY)).toBeNull()
    expect(sidebarWidth.read()).toBe(248)
  })

  it('falls back to the default and ignores failures when localStorage throws', () => {
    const getSpy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('storage blocked')
    })
    const setSpy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('quota exceeded')
    })
    const removeSpy = vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new Error('storage blocked')
    })
    try {
      expect(sidebarWidth.read()).toBe(248)
      expect(() => sidebarWidth.write(320)).not.toThrow()
      expect(() => sidebarWidth.clear()).not.toThrow()
    } finally {
      getSpy.mockRestore()
      setSpy.mockRestore()
      removeSpy.mockRestore()
    }
  })
})
