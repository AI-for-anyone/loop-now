// The fixed color pairs for Plate avatars: blue/pink, green/blue, pink/yellow, yellow/blue.
export const AVATAR_PAIRS = [
  ['var(--riso-blue)', 'var(--riso-pink)'],
  ['var(--green)', 'var(--riso-blue)'],
  ['var(--riso-pink)', 'var(--riso-yellow)'],
  ['var(--riso-yellow)', 'var(--riso-blue)'],
] as const

export interface TranslucentAvatarPair {
  name: string
  light: readonly [back: string, front: string]
  dark: readonly [back: string, front: string]
}

// Curated translucent optical-lens palettes for the default mono skin (10 harmonious pairs).
// Each circle carries alpha transparency so the overlapping Venn lens naturally blends into a
// luminous third tone on both pure white light surfaces and obsidian dark surfaces.
export const TRANSLUCENT_AVATAR_PAIRS: readonly TranslucentAvatarPair[] = [
  {
    name: 'cobalt-coral',
    light: ['rgba(59, 130, 246, 0.72)', 'rgba(244, 63, 94, 0.68)'],
    dark: ['rgba(96, 165, 250, 0.76)', 'rgba(251, 113, 133, 0.72)'],
  },
  {
    name: 'emerald-cyan',
    light: ['rgba(16, 185, 129, 0.72)', 'rgba(6, 182, 212, 0.68)'],
    dark: ['rgba(52, 211, 153, 0.76)', 'rgba(34, 211, 238, 0.72)'],
  },
  {
    name: 'violet-rose',
    light: ['rgba(139, 92, 246, 0.72)', 'rgba(236, 72, 153, 0.68)'],
    dark: ['rgba(167, 139, 250, 0.76)', 'rgba(244, 114, 182, 0.72)'],
  },
  {
    name: 'amber-vermilion',
    light: ['rgba(245, 158, 11, 0.74)', 'rgba(239, 68, 68, 0.68)'],
    dark: ['rgba(251, 191, 36, 0.78)', 'rgba(248, 113, 113, 0.72)'],
  },
  {
    name: 'indigo-teal',
    light: ['rgba(99, 102, 241, 0.72)', 'rgba(20, 184, 166, 0.68)'],
    dark: ['rgba(129, 140, 248, 0.76)', 'rgba(45, 212, 191, 0.72)'],
  },
  {
    name: 'magenta-amber',
    light: ['rgba(217, 70, 239, 0.70)', 'rgba(245, 158, 11, 0.72)'],
    dark: ['rgba(232, 121, 249, 0.74)', 'rgba(251, 191, 36, 0.76)'],
  },
  {
    name: 'sky-indigo',
    light: ['rgba(14, 165, 233, 0.72)', 'rgba(79, 70, 229, 0.68)'],
    dark: ['rgba(56, 189, 248, 0.76)', 'rgba(129, 140, 248, 0.72)'],
  },
  {
    name: 'lime-emerald',
    light: ['rgba(132, 204, 22, 0.74)', 'rgba(16, 185, 129, 0.68)'],
    dark: ['rgba(163, 230, 53, 0.78)', 'rgba(52, 211, 153, 0.72)'],
  },
  {
    name: 'tangerine-orchid',
    light: ['rgba(249, 115, 22, 0.72)', 'rgba(168, 85, 247, 0.68)'],
    dark: ['rgba(251, 146, 60, 0.76)', 'rgba(192, 132, 252, 0.72)'],
  },
  {
    name: 'slate-cobalt',
    light: ['rgba(100, 116, 139, 0.68)', 'rgba(37, 99, 235, 0.70)'],
    dark: ['rgba(148, 163, 184, 0.72)', 'rgba(96, 165, 250, 0.76)'],
  },
] as const

function fnv1a(id: string): number {
  let hash = 0x811c9dc5
  for (let i = 0; i < id.length; i++) {
    hash ^= id.charCodeAt(i)
    hash = Math.imul(hash, 0x01000193) >>> 0
  }
  return hash
}

// FNV-1a hash of the Agent ID mapped to a stable Plate variant index (0..3).
export function avatarVariant(id: string): 0 | 1 | 2 | 3 {
  return (fnv1a(id) % AVATAR_PAIRS.length) as 0 | 1 | 2 | 3
}

// The same ID always gets the same Plate color pair.
export function avatarPair(id: string): (typeof AVATAR_PAIRS)[number] {
  return AVATAR_PAIRS[avatarVariant(id)]
}

// The same ID always gets the same curated translucent color pair for mono light/dark themes.
export function translucentAvatarPalette(id: string): TranslucentAvatarPair {
  return TRANSLUCENT_AVATAR_PAIRS[fnv1a(id) % TRANSLUCENT_AVATAR_PAIRS.length]
}

export function translucentAvatarPair(id: string, theme: 'light' | 'dark'): readonly [back: string, front: string] {
  return translucentAvatarPalette(id)[theme]
}

export interface ProjectAvatarPalette {
  name: string
  light: { bg: string; fg: string }
  dark: { bg: string; fg: string }
}

// Curated per-project monogram palettes (10 distinct hues) so every project has a recognizable colored emblem.
export const PROJECT_AVATAR_PALETTES: readonly ProjectAvatarPalette[] = [
  { name: 'cobalt', light: { bg: 'rgba(0, 102, 219, 0.14)', fg: '#0055BA' }, dark: { bg: 'rgba(59, 158, 255, 0.22)', fg: '#7CC0FF' } },
  { name: 'violet', light: { bg: 'rgba(121, 40, 202, 0.13)', fg: '#6B21A8' }, dark: { bg: 'rgba(168, 85, 247, 0.22)', fg: '#D8B4FE' } },
  { name: 'teal', light: { bg: 'rgba(13, 148, 136, 0.14)', fg: '#0F766E' }, dark: { bg: 'rgba(45, 212, 191, 0.20)', fg: '#5EEAD4' } },
  { name: 'amber', light: { bg: 'rgba(217, 119, 6, 0.15)', fg: '#B45309' }, dark: { bg: 'rgba(251, 191, 36, 0.20)', fg: '#FCD34D' } },
  { name: 'rose', light: { bg: 'rgba(225, 29, 72, 0.13)', fg: '#BE123C' }, dark: { bg: 'rgba(251, 113, 133, 0.22)', fg: '#FDA4AF' } },
  { name: 'indigo', light: { bg: 'rgba(79, 70, 229, 0.14)', fg: '#4338CA' }, dark: { bg: 'rgba(129, 140, 248, 0.22)', fg: '#A5B4FC' } },
  { name: 'emerald', light: { bg: 'rgba(16, 185, 129, 0.14)', fg: '#047857' }, dark: { bg: 'rgba(52, 211, 153, 0.20)', fg: '#6EE7B7' } },
  { name: 'orange', light: { bg: 'rgba(234, 88, 12, 0.14)', fg: '#C2410C' }, dark: { bg: 'rgba(251, 146, 60, 0.22)', fg: '#FDBA74' } },
  { name: 'sky', light: { bg: 'rgba(2, 132, 199, 0.14)', fg: '#0369A1' }, dark: { bg: 'rgba(56, 189, 248, 0.22)', fg: '#7DD3FC' } },
  { name: 'fuchsia', light: { bg: 'rgba(192, 38, 211, 0.13)', fg: '#A21CAF' }, dark: { bg: 'rgba(232, 121, 249, 0.22)', fg: '#F0ABFC' } },
] as const

// Extract a crisp 1-2 character monogram from a project name:
// - Multi-word / kebab / snake / camel names take the initials of the first two segments (e.g. "loop-now" -> "LN", "docsSite" -> "DS").
// - Single ASCII words take the first two letters ("backend" -> "BA").
// - CJK or non-Latin names take the first character ("默认项目" -> "默").
export function projectMonogram(name: string): string {
  const trimmed = name.trim()
  if (!trimmed) return '?'
  const firstChar = Array.from(trimmed)[0] ?? '?'
  if (!/[A-Za-z0-9]/.test(firstChar)) return firstChar
  const parts = trimmed
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .split(/[\s\-_./]+/)
    .filter(Boolean)
  if (parts.length >= 2) {
    return `${Array.from(parts[0])[0] ?? ''}${Array.from(parts[1])[0] ?? ''}`.toUpperCase()
  }
  return Array.from(parts[0] ?? trimmed).slice(0, 2).join('').toUpperCase()
}

export function projectAvatarPalette(key: string): ProjectAvatarPalette {
  return PROJECT_AVATAR_PALETTES[fnv1a(key) % PROJECT_AVATAR_PALETTES.length]
}
