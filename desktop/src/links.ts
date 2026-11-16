// How navigation in the window is handled: the server's own origin stays in the window, http, https and mailto go to the system browser,
// and everything else is ignored.
// New windows (window.open, target=_blank) pass no appOrigin, so even same-origin pages open in the system browser.
export type LinkAction = 'app' | 'external' | 'deny'

const externalProtocols = new Set(['http:', 'https:', 'mailto:'])

export function classifyNavigation(url: string, appOrigin?: string): LinkAction {
  let parsed: URL
  try {
    parsed = new URL(url)
  } catch {
    return 'deny'
  }
  if (appOrigin !== undefined && parsed.origin === appOrigin) return 'app'
  return externalProtocols.has(parsed.protocol) ? 'external' : 'deny'
}

// An authorization popup is handed to the system browser only once: a single navigation fires did-start-navigation and then will-navigate.
// The first address that is not about:blank is handed off; later navigations in this popup are not handed off (it is being closed).
export function handOffOnce(handOff: (url: string) => void): (url: string) => void {
  let done = false
  return (url) => {
    if (done || url === 'about:blank') return
    done = true
    handOff(url)
  }
}
