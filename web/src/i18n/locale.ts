// The console's UI language, decided once when the page loads: ?lang= in the address (handy for screenshots) comes
// first, then the choice saved for this browser in global settings, and last the browser language: Chinese if it
// starts with zh, English otherwise. Changing the language in global settings saves the choice and reloads the page.
export const locales = ['zh-CN', 'en-US'] as const
export type Locale = (typeof locales)[number]

// Language names are written in their own language and don't change with the UI language.
export const localeNames: Record<Locale, string> = { 'zh-CN': '中文', 'en-US': 'English' }

export const localeStorageKey = 'loop-now.locale'

const isLocale = (value: string | null): value is Locale => locales.includes(value as Locale)

// ?lang= also accepts short forms such as zh and en.
function fromParam(value: string | null): Locale | null {
  if (!value) return null
  if (isLocale(value)) return value
  const lower = value.toLowerCase()
  return lower.startsWith('zh') ? 'zh-CN' : lower.startsWith('en') ? 'en-US' : null
}

export function resolveLocale(search: string, saved: string | null, languages: readonly string[]): Locale {
  const param = fromParam(new URLSearchParams(search).get('lang'))
  if (param) return param
  if (isLocale(saved)) return saved
  return languages[0]?.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en-US'
}

// Browser storage may be unavailable (private mode, disabled); when reading or writing fails, treat it as if nothing was saved.
function readSaved(): string | null {
  try {
    return window.localStorage.getItem(localeStorageKey)
  } catch {
    return null
  }
}

export const locale: Locale = resolveLocale(window.location.search, readSaved(), navigator.languages?.length ? navigator.languages : [navigator.language])

// Reloads the page; tests replace it.
export const page = { reload: () => window.location.reload() }

export function chooseLocale(next: Locale) {
  try {
    window.localStorage.setItem(localeStorageKey, next)
  } catch {
    // If it can't be saved, only this one time is affected: without ?lang=
    // in the address, the reloaded page still follows the browser language.
  }
  // ?lang= in the address takes precedence over the saved choice, so remove it as well when changing the language.
  const url = new URL(window.location.href)
  if (url.searchParams.has('lang')) {
    url.searchParams.delete('lang')
    window.history.replaceState(window.history.state, '', url)
  }
  page.reload()
}

