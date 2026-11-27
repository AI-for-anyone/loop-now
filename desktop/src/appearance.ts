import type { DesktopAppearance } from './bridge'

export type ResolvedAppearance = {
  skin: 'mono' | 'plate'
  theme: 'light' | 'dark'
  backgroundColor: string
}

const BACKGROUNDS: Record<'mono-light' | 'mono-dark' | 'plate', string> = {
  'mono-light': '#FFFFFF',
  'mono-dark': '#000000',
  plate: '#F3EFE6',
}

// Resolve the effective window background and status-page palette from saved console appearance and current OS dark mode.
// When mode is "system", always recompute from systemDark so a stale saved backgroundColor never disagrees with the OS theme.
export function resolveWindowAppearance(saved: DesktopAppearance | undefined, systemDark: boolean): ResolvedAppearance {
  if (saved?.skin === 'plate') {
    return { skin: 'plate', theme: 'light', backgroundColor: BACKGROUNDS.plate }
  }
  const mode = saved?.mode ?? 'system'
  const theme: 'light' | 'dark' = mode === 'system' ? (systemDark ? 'dark' : 'light') : mode
  return {
    skin: 'mono',
    theme,
    backgroundColor: theme === 'dark' ? BACKGROUNDS['mono-dark'] : BACKGROUNDS['mono-light'],
  }
}

export function nativeThemeSource(saved: DesktopAppearance | undefined): 'system' | 'light' | 'dark' {
  if (!saved) return 'system'
  if (saved.skin === 'plate') return 'light'
  return saved.mode
}
