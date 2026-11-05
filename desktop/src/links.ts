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

// 授权弹窗只交给系统浏览器一次：同一次导航会先后触发 did-start-navigation 与 will-navigate，
// 第一个不是 about:blank 的地址交出去，之后这个弹窗上的导航都不再交（它正被关掉）。
export function handOffOnce(handOff: (url: string) => void): (url: string) => void {
  let done = false
  return (url) => {
    if (done || url === 'about:blank') return
    done = true
    handOff(url)
  }
}
