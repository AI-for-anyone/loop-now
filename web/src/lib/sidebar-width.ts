// Desktop sidebar width bounds and per-browser persistence. The narrow phone drawer keeps its own fixed width and does not use this.
export const SIDEBAR_WIDTH_STORAGE_KEY = 'loop-now.sidebarWidth'
export const SIDEBAR_WIDTH_DEFAULT = 248
export const SIDEBAR_WIDTH_MIN = 200
export const SIDEBAR_WIDTH_MAX = 420
export const SIDEBAR_WIDTH_MAX_RATIO = 0.4
export const SIDEBAR_WIDTH_STEP = 16

// At most 420px and never more than 40% of the viewport width; never drops below the minimum so ARIA bounds stay valid.
export function maxSidebarWidth(viewportWidth: number): number {
  if (!Number.isFinite(viewportWidth) || viewportWidth <= 0) return SIDEBAR_WIDTH_MAX
  return Math.max(SIDEBAR_WIDTH_MIN, Math.min(SIDEBAR_WIDTH_MAX, Math.floor(viewportWidth * SIDEBAR_WIDTH_MAX_RATIO)))
}

export function clampSidebarWidth(width: number, viewportWidth: number): number {
  const max = maxSidebarWidth(viewportWidth)
  if (!Number.isFinite(width)) return Math.min(max, Math.max(SIDEBAR_WIDTH_MIN, SIDEBAR_WIDTH_DEFAULT))
  return Math.min(max, Math.max(SIDEBAR_WIDTH_MIN, Math.round(width)))
}

export function parseSidebarWidth(raw: string | null): number {
  if (raw === null || raw.trim() === '') return SIDEBAR_WIDTH_DEFAULT
  const parsed = Number(raw)
  if (!Number.isFinite(parsed) || parsed <= 0) return SIDEBAR_WIDTH_DEFAULT
  return Math.min(SIDEBAR_WIDTH_MAX, Math.max(SIDEBAR_WIDTH_MIN, Math.round(parsed)))
}

export const sidebarWidth = {
  read(): number {
    try {
      return parseSidebarWidth(window.localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY))
    } catch {
      return SIDEBAR_WIDTH_DEFAULT
    }
  },
  write(width: number) {
    const clamped = Math.min(SIDEBAR_WIDTH_MAX, Math.max(SIDEBAR_WIDTH_MIN, Math.round(width)))
    if (!Number.isFinite(clamped)) return
    try {
      window.localStorage.setItem(SIDEBAR_WIDTH_STORAGE_KEY, String(clamped))
    } catch {
      // When browser storage is unavailable, the resize still works for this session without failing.
    }
  },
  clear() {
    try {
      window.localStorage.removeItem(SIDEBAR_WIDTH_STORAGE_KEY)
    } catch {
      // Same as above.
    }
  },
}
