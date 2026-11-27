import { useSyncExternalStore } from 'react'
import {
  DEFAULT_SKIN,
  DEFAULT_THEME_MODE,
  isSkinId,
  isThemeMode,
  resolveTheme,
  skinSupportsDark,
  skinWindowBackground,
  type ResolvedTheme,
  type SkinId,
  type ThemeMode,
} from './skins'

export const SKIN_STORAGE_KEY = 'loop-now.skin'
export const THEME_STORAGE_KEY = 'loop-now.theme'

export type ThemeSnapshot = {
  readonly skin: SkinId
  readonly mode: ThemeMode
  readonly resolvedTheme: ResolvedTheme
  readonly supportsDark: boolean
}

const DARK_MEDIA_QUERY = '(prefers-color-scheme: dark)'

function readStorage(key: string): string | null {
  try {
    return window.localStorage.getItem(key)
  } catch {
    return null
  }
}

function writeStorage(key: string, value: string): void {
  try {
    window.localStorage.setItem(key, value)
  } catch {
    // Private browsing or disabled storage: keep the change active for this session.
  }
}

function clearQueryParam(name: string): void {
  try {
    const url = new URL(window.location.href)
    if (!url.searchParams.has(name)) return
    url.searchParams.delete(name)
    window.history.replaceState(null, '', url.toString())
  } catch {
    // Ignore history errors in restricted frames.
  }
}

function queryParam(name: string): string | null {
  try {
    return new URLSearchParams(window.location.search).get(name)
  } catch {
    return null
  }
}

function readSystemDark(): boolean {
  try {
    return typeof window.matchMedia === 'function' && window.matchMedia(DARK_MEDIA_QUERY).matches
  } catch {
    return false
  }
}

function readInitialSkin(): SkinId {
  const fromQuery = queryParam('skin')
  if (isSkinId(fromQuery)) return fromQuery
  const fromStorage = readStorage(SKIN_STORAGE_KEY)
  if (isSkinId(fromStorage)) return fromStorage
  return DEFAULT_SKIN
}

function readInitialMode(): ThemeMode {
  const fromQuery = queryParam('theme')
  if (isThemeMode(fromQuery)) return fromQuery
  const fromStorage = readStorage(THEME_STORAGE_KEY)
  if (isThemeMode(fromStorage)) return fromStorage
  return DEFAULT_THEME_MODE
}

function buildSnapshot(skin: SkinId, mode: ThemeMode, systemDark: boolean): ThemeSnapshot {
  return {
    skin,
    mode,
    resolvedTheme: resolveTheme(skin, mode, systemDark),
    supportsDark: skinSupportsDark(skin),
  }
}

let currentSkin: SkinId = readInitialSkin()
let currentMode: ThemeMode = readInitialMode()
let currentSystemDark: boolean = readSystemDark()
let snapshot: ThemeSnapshot = buildSnapshot(currentSkin, currentMode, currentSystemDark)
const listeners = new Set<() => void>()
let mediaCleanup: (() => void) | null = null

export function applyThemeToDocument(next: ThemeSnapshot): void {
  if (typeof document === 'undefined') return
  const root = document.documentElement
  root.dataset.skin = next.skin
  root.dataset.theme = next.resolvedTheme
  root.classList.toggle('dark', next.resolvedTheme === 'dark')
  root.style.colorScheme = next.resolvedTheme

  const bridge = typeof window !== 'undefined' ? window.loopNowDesktop : undefined
  if (typeof bridge?.setAppearance === 'function') {
    bridge.setAppearance({
      skin: next.skin,
      mode: next.supportsDark ? next.mode : 'light',
      resolvedTheme: next.resolvedTheme,
      backgroundColor: skinWindowBackground(next.skin, next.resolvedTheme),
    })
  }
}

function commit(): void {
  const next = buildSnapshot(currentSkin, currentMode, currentSystemDark)
  if (
    next.skin === snapshot.skin &&
    next.mode === snapshot.mode &&
    next.resolvedTheme === snapshot.resolvedTheme &&
    next.supportsDark === snapshot.supportsDark
  ) {
    applyThemeToDocument(next)
    return
  }
  snapshot = next
  applyThemeToDocument(next)
  for (const listener of listeners) listener()
}

function attachMediaListener(): void {
  if (mediaCleanup || typeof window === 'undefined' || typeof window.matchMedia !== 'function') return
  const query = window.matchMedia(DARK_MEDIA_QUERY)
  const onChange = (event: MediaQueryListEvent) => {
    currentSystemDark = event.matches
    commit()
  }
  query.addEventListener('change', onChange)
  mediaCleanup = () => {
    query.removeEventListener('change', onChange)
    mediaCleanup = null
  }
}

export function initTheme(): ThemeSnapshot {
  currentSkin = readInitialSkin()
  currentMode = readInitialMode()
  currentSystemDark = readSystemDark()
  snapshot = buildSnapshot(currentSkin, currentMode, currentSystemDark)
  applyThemeToDocument(snapshot)
  attachMediaListener()
  return snapshot
}

export function setSkin(skin: SkinId): void {
  if (!isSkinId(skin)) return
  writeStorage(SKIN_STORAGE_KEY, skin)
  clearQueryParam('skin')
  currentSkin = skin
  commit()
}

export function setThemeMode(mode: ThemeMode): void {
  if (!isThemeMode(mode)) return
  writeStorage(THEME_STORAGE_KEY, mode)
  clearQueryParam('theme')
  currentMode = mode
  commit()
}

export function getThemeSnapshot(): ThemeSnapshot {
  return snapshot
}

export function subscribeTheme(listener: () => void): () => void {
  listeners.add(listener)
  attachMediaListener()
  return () => {
    listeners.delete(listener)
  }
}

export function useTheme(): ThemeSnapshot & {
  setSkin: (skin: SkinId) => void
  setThemeMode: (mode: ThemeMode) => void
} {
  const current = useSyncExternalStore(subscribeTheme, getThemeSnapshot, getThemeSnapshot)
  return { ...current, setSkin, setThemeMode }
}

// Subscribe only to the active skin id so toggling light/dark does not re-render skin-dependent trees.
export function useSkin(): SkinId {
  return useSyncExternalStore(subscribeTheme, () => getThemeSnapshot().skin, () => getThemeSnapshot().skin)
}

// Reset module state between unit tests after localStorage/URL changes.
export function resetThemeForTest(): void {
  mediaCleanup?.()
  currentSkin = readInitialSkin()
  currentMode = readInitialMode()
  currentSystemDark = readSystemDark()
  snapshot = buildSnapshot(currentSkin, currentMode, currentSystemDark)
  applyThemeToDocument(snapshot)
}
