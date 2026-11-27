// Skin and color-mode registry for the console.
// "mono" (Swiss high-contrast monochrome) is the default skin and supports light, dark, and system modes.
// "plate" preserves the original Risograph print aesthetic (light mode only).

export const skinIds = ['mono', 'plate'] as const
export type SkinId = (typeof skinIds)[number]

export const themeModes = ['system', 'light', 'dark'] as const
export type ThemeMode = (typeof themeModes)[number]

export type ResolvedTheme = 'light' | 'dark'

export type SkinDefinition = {
  readonly id: SkinId
  readonly supportedModes: readonly ResolvedTheme[]
  readonly backgrounds: Readonly<Record<ResolvedTheme, string>>
}

export const DEFAULT_SKIN: SkinId = 'mono'
export const DEFAULT_THEME_MODE: ThemeMode = 'system'

export const SKINS: Readonly<Record<SkinId, SkinDefinition>> = {
  mono: {
    id: 'mono',
    supportedModes: ['light', 'dark'],
    backgrounds: { light: '#FFFFFF', dark: '#000000' },
  },
  plate: {
    id: 'plate',
    supportedModes: ['light'],
    backgrounds: { light: '#F3EFE6', dark: '#F3EFE6' },
  },
}

export function isSkinId(value: string | null | undefined): value is SkinId {
  return value === 'mono' || value === 'plate'
}

export function isThemeMode(value: string | null | undefined): value is ThemeMode {
  return value === 'system' || value === 'light' || value === 'dark'
}

export function skinSupportsDark(skin: SkinId): boolean {
  return SKINS[skin].supportedModes.includes('dark')
}

export function resolveTheme(skin: SkinId, mode: ThemeMode, systemDark: boolean): ResolvedTheme {
  if (!skinSupportsDark(skin)) return 'light'
  if (mode === 'system') return systemDark ? 'dark' : 'light'
  return mode
}

export function skinWindowBackground(skin: SkinId, resolved: ResolvedTheme): string {
  return SKINS[skin].backgrounds[resolved]
}
