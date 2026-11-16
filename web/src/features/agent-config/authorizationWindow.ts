import { t } from '@/i18n'
export function reserveAuthorizationWindow() {
  let popup: Window | null = null
  let navigated = false
  let unusedClosed = false
  const closeUnused = () => {
    // Whether the provider's window is closed must never be taken as a sign that the login succeeded or failed.
    // A window that has already navigated is never closed; the failure cleanup may be called repeatedly and does not propagate
    // WindowProxy exceptions.
    try {
      if (!navigated && !unusedClosed && popup && !popup.closed) {
        unusedClosed = true
        popup.close()
      }
    } catch { /* The user or the browser may already have taken the window away. */ }
  }
  try {
    popup = window.open('about:blank', '_blank')
    if (popup) {
      popup.opener = null
      // The initial async wait gets clear feedback too; only fixed text goes here, never the authorization URL or state secrets.
      try {
        if (popup.document?.body) {
          popup.document.title = t.credentials.authWindow.title
          popup.document.body.textContent = t.credentials.authWindow.body
        }
      } catch { /* The console's native continue link does not depend on writing to the placeholder document succeeding. */ }
    }
  } catch {
    closeUnused()
    popup = null
  }
  const failed = () => { closeUnused(); return false }
  return {
    navigate(url: string): boolean {
      try {
        if (new URL(url).protocol !== 'https:' || !popup || popup.closed) return failed()
        popup.location.replace(url)
        navigated = true
        return true
      } catch { return failed() }
    },
    closeUnused,
  }
}
