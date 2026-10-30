// 控制台的界面语言，在页面加载时确定一次：地址里的 ?lang=（便于截图）优先，其次是全局设置里为这个浏览器保存的选择，
// 最后按浏览器语言，zh 开头用中文，其余用英文。在全局设置里换语言会保存选择并重新加载页面。
export const locales = ['zh-CN', 'en-US'] as const
export type Locale = (typeof locales)[number]

// 语言名用各自的语言写，不随界面语言变化。
export const localeNames: Record<Locale, string> = { 'zh-CN': '中文', 'en-US': 'English' }

export const localeStorageKey = 'loop-now.locale'

const isLocale = (value: string | null): value is Locale => locales.includes(value as Locale)

// ?lang= 也接受 zh、en 这样的简写。
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

// 浏览器存储可能不可用（隐私模式、被禁用），读写失败时按没有保存处理。
function readSaved(): string | null {
  try {
    return window.localStorage.getItem(localeStorageKey)
  } catch {
    return null
  }
}

export const locale: Locale = resolveLocale(window.location.search, readSaved(), navigator.languages?.length ? navigator.languages : [navigator.language])

// 重新加载页面；测试里替换它。
export const page = { reload: () => window.location.reload() }

export function chooseLocale(next: Locale) {
  try {
    window.localStorage.setItem(localeStorageKey, next)
  } catch {
    // 存不下时只影响这一次：地址里没有 ?lang= 时，重新加载后仍按浏览器语言。
  }
  // 地址里的 ?lang= 优先于保存的选择，换语言时一并去掉。
  const url = new URL(window.location.href)
  if (url.searchParams.has('lang')) {
    url.searchParams.delete('lang')
    window.history.replaceState(window.history.state, '', url)
  }
  page.reload()
}

// 服务端返回的说明文字（错误说明、字段校验、执行失败原因）只有中文。界面不是中文时，
// 有错误码文案的地方只显示文案表里的说明，不再附上服务端原文；没有错误码文案的原样显示。
export const showServerText = locale === 'zh-CN'
