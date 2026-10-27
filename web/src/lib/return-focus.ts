import { useRef } from 'react'

// Radix 的对话框与抽屉关闭时只把焦点还给它的 Trigger；控制台的对话框和抽屉都由状态或地址控制开合，
// 没有 Trigger，焦点会落到页面主体上。这里记下打开前获得焦点的元素，关闭时还给它。
// 调用方自己的处理先执行；调用方已阻止默认行为时不覆盖。
export function useReturnFocus(onOpenAutoFocus?: (event: Event) => void, onCloseAutoFocus?: (event: Event) => void) {
  const target = useRef<HTMLElement | null>(null)
  return {
    onOpenAutoFocus: (event: Event) => {
      target.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
      onOpenAutoFocus?.(event)
    },
    onCloseAutoFocus: (event: Event) => {
      onCloseAutoFocus?.(event)
      if (event.defaultPrevented) return
      event.preventDefault()
      if (target.current?.isConnected) target.current.focus()
    },
  }
}
