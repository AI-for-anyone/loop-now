import { t } from '@/i18n'
export function reserveAuthorizationWindow() {
  let popup: Window | null = null
  let navigated = false
  let unusedClosed = false
  const closeUnused = () => {
    // 供应商窗口关闭与否不能被当作登录成功或失败的依据。
    // 已导航窗口不关闭；失败清理可重复调用，也不传播WindowProxy异常。
    try {
      if (!navigated && !unusedClosed && popup && !popup.closed) {
        unusedClosed = true
        popup.close()
      }
    } catch { /* 窗口可能已由用户或浏览器收回。 */ }
  }
  try {
    popup = window.open('about:blank', '_blank')
    if (popup) {
      popup.opener = null
      // 初始异步等待也有明确反馈；这里仅固定文字，不写授权URL/状态秘密。
      try {
        if (popup.document?.body) {
          popup.document.title = t.credentials.authWindow.title
          popup.document.body.textContent = t.credentials.authWindow.body
        }
      } catch { /* 不依赖占位文档写入成功才能提供控制台原生继续入口。 */ }
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
