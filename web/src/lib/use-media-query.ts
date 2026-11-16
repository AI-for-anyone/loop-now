import { useSyncExternalStore } from 'react'

// 当前窗口是否符合一条媒体查询（例如窄屏），窗口变化时重新渲染。测试环境里的 matchMedia 一律不匹配。
export function useMediaQuery(query: string): boolean {
  return useSyncExternalStore(
    (onChange) => {
      const list = window.matchMedia(query)
      list.addEventListener('change', onChange)
      return () => list.removeEventListener('change', onChange)
    },
    () => window.matchMedia(query).matches,
    () => false,
  )
}

// 左侧栏收进抽屉、票根隐藏的窄屏（与 Tailwind 的 max-nav 一致）。
export const narrowQuery = '(width < 56.3125rem)'
