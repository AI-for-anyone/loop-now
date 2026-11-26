// Content the page sends through preload (untrusted): system notifications and the Dock unread count. Anything malformed is dropped.
export type DesktopNotification = { id: string; title: string; body: string; path: string }
export type DesktopAppearance = {
  skin: string
  mode: 'system' | 'light' | 'dark'
  resolvedTheme: 'light' | 'dark'
  backgroundColor: string
}

const text = (value: unknown, max: number) => (typeof value === 'string' ? value.slice(0, max) : undefined)

// path is an address inside the console: it starts with a single / and has no backslash; once the notification is clicked, the page's own
// router navigates to it.
export function parseNotification(value: unknown): DesktopNotification | undefined {
  if (!value || typeof value !== 'object') return undefined
  const v = value as Record<string, unknown>
  const id = text(v.id, 200)
  const title = text(v.title, 200)
  const body = text(v.body, 500)
  const path = v.path
  if (!id || !title || body === undefined || typeof path !== 'string') return undefined
  if (!path.startsWith('/') || path.startsWith('//') || path.includes('\\') || path.length > 2000) return undefined
  return { id, title, body, path }
}

export function parseBadge(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isInteger(value) && value >= 0 ? Math.min(value, 99_999) : undefined
}

const hexColor = /^#[0-9A-Fa-f]{6}$/

export function parseAppearance(value: unknown): DesktopAppearance | undefined {
  if (!value || typeof value !== 'object') return undefined
  const v = value as Record<string, unknown>
  const skin = text(v.skin, 40)
  const mode = v.mode
  const resolvedTheme = v.resolvedTheme
  const backgroundColor = v.backgroundColor
  if (!skin) return undefined
  if (mode !== 'system' && mode !== 'light' && mode !== 'dark') return undefined
  if (resolvedTheme !== 'light' && resolvedTheme !== 'dark') return undefined
  if (typeof backgroundColor !== 'string' || !hexColor.test(backgroundColor)) return undefined
  return { skin, mode, resolvedTheme, backgroundColor }
}
