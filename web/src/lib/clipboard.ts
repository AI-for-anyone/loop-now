// 把文字写进剪贴板，写不进去时返回 false，不抛错。
// navigator.clipboard 只在安全上下文里有（用 http 的局域网地址打开控制台时没有），并且 Safari 只允许在按键、点击的手势里写；
// 网络请求回来之后再写就会被拒绝。这两种情况退到旧的复制命令（同样可能因为不在手势内而失败）。
export async function copyText(text: string): Promise<boolean> {
  try {
    if (typeof navigator.clipboard?.writeText === 'function') {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // 被浏览器拒绝：试旧的复制命令。
  }
  return legacyCopy(text)
}

function legacyCopy(text: string): boolean {
  if (typeof document.execCommand !== 'function') return false
  const focused = document.activeElement instanceof HTMLElement ? document.activeElement : null
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.setAttribute('aria-hidden', 'true')
  area.style.cssText = 'position:fixed;top:0;left:0;opacity:0;pointer-events:none'
  document.body.appendChild(area)
  // 复制命令复制的是当前选中的内容：临时输入框要先拿到焦点再全选，用完把焦点还回去。
  area.focus({ preventScroll: true })
  area.select()
  try {
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    area.remove()
    focused?.focus({ preventScroll: true })
  }
}
