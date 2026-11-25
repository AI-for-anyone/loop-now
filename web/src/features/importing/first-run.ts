// The import page opens by itself the first time the console opens with no Agents at all, once: skipping it is remembered in this
// browser (Settings opens it again any time). Storage can be unavailable (a private window); then the page offers itself again.
const KEY = 'loop-now.importSkipped'

export function firstRunImportSkipped(): boolean {
  try {
    return window.localStorage.getItem(KEY) === '1'
  } catch {
    return false
  }
}

export function skipFirstRunImport(): void {
  try {
    window.localStorage.setItem(KEY, '1')
  } catch {
    // Nothing to remember it in; the page may open again next time.
  }
}

// The page opened by itself in this browser session already: going back to the overview does not bring it back (a scan that keeps
// failing must not trap the person on it).
const SHOWN = 'loop-now.importShown'

export function firstRunImportShown(): boolean {
  try {
    return window.sessionStorage.getItem(SHOWN) === '1'
  } catch {
    return false
  }
}

export function markFirstRunImportShown(): void {
  try {
    window.sessionStorage.setItem(SHOWN, '1')
  } catch {
    // Nothing to remember it in.
  }
}
