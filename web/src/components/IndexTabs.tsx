import type { ComponentProps } from 'react'
import { NavLink, type NavLinkProps } from 'react-router'
import { indexTabClass } from '@/lib/index-tab'
import { cn } from '@/lib/utils'

// 一排索引标签，紧贴在下面那张纸的上沿：左侧缩进 26px，底边对齐，当前页的标签高出一截。
// 窄屏时放不下可以横向滚动，不把整页撑宽；上方留 6px，滚动容器不会裁掉标签的焦点环。
export function IndexTabs({ className, ...props }: ComponentProps<'nav'>) {
  return (
    <nav
      className={cn('relative z-[1] flex shrink-0 items-end gap-1.5 pl-[26px] max-nav:overflow-x-auto max-nav:pt-1.5 max-nav:pl-3 max-nav:no-scrollbar', className)}
      {...props}
    />
  )
}

// 页签：地址匹配时是当前页（aria-current=page）。
export function IndexTabLink({ className, ...props }: Omit<NavLinkProps, 'className'> & { className?: string }) {
  return <NavLink className={({ isActive }) => cn(indexTabClass(isActive), className)} {...props} />
}
