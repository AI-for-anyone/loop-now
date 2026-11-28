import { t } from '@/i18n'

// Relay sign-in pages are normally https://relay.loop-now.app/connect; local development and tests may point LOOP_NOW_RELAY_URL at a
// loopback http address (127.0.0.1, localhost or ::1), which the server itself also restricts to loopback.
export function isAllowedRelaySignInUrl(raw: string): boolean {
  try {
    const parsed = new URL(raw)
    if (parsed.protocol === 'https:') return true
    if (parsed.protocol === 'http:') {
      return parsed.hostname === '127.0.0.1' || parsed.hostname === 'localhost' || parsed.hostname === '::1' || parsed.hostname === '[::1]'
    }
    return false
  } catch {
    return false
  }
}

// Reserves a blank tab/window synchronously inside a click handler so browsers do not block the popup and Electron's desktop shell
// hands the navigated URL off to shell.openExternal once POST /relay-account/sign-in returns.
export function reserveRelaySignInWindow() {
  let popup: Window | null = null
  let navigated = false
  let unusedClosed = false

  const closeUnused = () => {
    try {
      if (!navigated && !unusedClosed && popup && !popup.closed) {
        unusedClosed = true
        popup.close()
      }
    } catch {
      // The browser or desktop shell may already have closed the window.
    }
  }

  try {
    popup = window.open('about:blank', '_blank')
    if (popup) {
      popup.opener = null
      try {
        if (popup.document?.body) {
          popup.document.title = t.devices.account.windowTitle
          popup.document.body.textContent = t.devices.account.windowBody
        }
      } catch {
        // Placeholder content is optional; navigation still works without it.
      }
    }
  } catch {
    closeUnused()
    popup = null
  }

  const failed = () => {
    closeUnused()
    return false
  }

  return {
    navigate(url: string): boolean {
      try {
        if (!isAllowedRelaySignInUrl(url) || !popup || popup.closed) return failed()
        popup.location.replace(url)
        navigated = true
        return true
      } catch {
        return failed()
      }
    },
    closeUnused,
  }
}
