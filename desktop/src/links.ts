// 窗口里的导航怎么处理：服务端自己的源留在窗口里，http、https、mailto 交给系统浏览器，其余一律不处理。
// 新窗口（window.open、target=_blank）不传 appOrigin，同源页面也在系统浏览器里打开。
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
