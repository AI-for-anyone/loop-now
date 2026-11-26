import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  getThemeSnapshot,
  initTheme,
  resetThemeForTest,
  setSkin,
  setThemeMode,
  SKIN_STORAGE_KEY,
  subscribeTheme,
  THEME_STORAGE_KEY,
} from './store'

function mockMatchMedia(initialMatches: boolean) {
  let matches = initialMatches
  const listeners = new Set<(event: MediaQueryListEvent) => void>()
  const original = window.matchMedia
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    get matches() {
      return matches
    },
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => {
      listeners.add(listener)
    },
    removeEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => {
      listeners.delete(listener)
    },
    dispatchEvent: () => false,
  }))
  return {
    setMatches(next: boolean) {
      matches = next
      for (const listener of listeners) listener({ matches: next } as MediaQueryListEvent)
    },
    restore() {
      window.matchMedia = original
    },
  }
}

describe('theme store', () => {
  afterEach(() => {
    window.history.replaceState(null, '', '/')
    window.localStorage.clear()
    delete window.loopNowDesktop
    resetThemeForTest()
  })

  it('defaults to mono skin and system color mode', () => {
    const snap = initTheme()
    expect(snap).toEqual({
      skin: 'mono',
      mode: 'system',
      resolvedTheme: 'light',
      supportsDark: true,
    })
    expect(document.documentElement.dataset.skin).toBe('mono')
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(document.documentElement.style.colorScheme).toBe('light')
  })

  it('persists skin and mode in localStorage and forces plate to light while preserving mode preference', () => {
    initTheme()
    setThemeMode('dark')
    expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBe('dark')
    expect(getThemeSnapshot().resolvedTheme).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)

    setSkin('plate')
    expect(window.localStorage.getItem(SKIN_STORAGE_KEY)).toBe('plate')
    expect(getThemeSnapshot()).toEqual({
      skin: 'plate',
      mode: 'dark',
      resolvedTheme: 'light',
      supportsDark: false,
    })
    expect(document.documentElement.dataset.skin).toBe('plate')
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)

    // Switching back to mono restores the remembered dark mode preference.
    setSkin('mono')
    expect(getThemeSnapshot().resolvedTheme).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('reads URL overrides (?skin= and ?theme=) and clears them when the user picks explicitly', () => {
    window.history.replaceState(null, '', '/?skin=plate&theme=dark')
    const snap = initTheme()
    expect(snap.skin).toBe('plate')
    expect(snap.mode).toBe('dark')
    expect(snap.resolvedTheme).toBe('light')

    setSkin('mono')
    expect(new URLSearchParams(window.location.search).has('skin')).toBe(false)
    expect(getThemeSnapshot().resolvedTheme).toBe('dark')

    setThemeMode('light')
    expect(new URLSearchParams(window.location.search).has('theme')).toBe(false)
    expect(getThemeSnapshot().resolvedTheme).toBe('light')
  })

  it('reacts to OS prefers-color-scheme changes when mode is system', () => {
    const media = mockMatchMedia(false)
    try {
      initTheme()
      const listener = vi.fn()
      const unsubscribe = subscribeTheme(listener)
      expect(getThemeSnapshot().resolvedTheme).toBe('light')

      media.setMatches(true)
      expect(getThemeSnapshot().resolvedTheme).toBe('dark')
      expect(document.documentElement.dataset.theme).toBe('dark')
      expect(listener).toHaveBeenCalledTimes(1)

      unsubscribe()
    } finally {
      media.restore()
    }
  })

  it('notifies desktop bridge when setAppearance is available', () => {
    const setAppearance = vi.fn()
    window.loopNowDesktop = {
      platform: 'darwin',
      notify: vi.fn(),
      setBadge: vi.fn(),
      onNavigate: vi.fn(() => () => {}),
      setAppearance,
    }
    initTheme()
    setThemeMode('dark')
    expect(setAppearance).toHaveBeenLastCalledWith({
      skin: 'mono',
      mode: 'dark',
      resolvedTheme: 'dark',
      backgroundColor: '#000000',
    })

    setSkin('plate')
    expect(setAppearance).toHaveBeenLastCalledWith({
      skin: 'plate',
      mode: 'light',
      resolvedTheme: 'light',
      backgroundColor: '#F3EFE6',
    })
  })
})
