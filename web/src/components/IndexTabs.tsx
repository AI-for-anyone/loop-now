import type { ComponentProps } from 'react'
import { NavLink, type NavLinkProps } from 'react-router'
import { indexTabClass, indexTabsClass } from '@/lib/index-tab'
import { cn } from '@/lib/utils'

// A row of index tabs sitting right on the top edge of the sheet below: indented 26px on the left, bottom edges aligned, and the current
// page's tab a step taller.
// On narrow screens, when they do not fit, they scroll horizontally instead of widening the whole page; 6px is left above so the scroll
// container does not clip the tabs' focus rings.
export function IndexTabs({ className, ...props }: ComponentProps<'nav'>) {
  return (
    <nav
      className={cn(indexTabsClass, className)}
      {...props}
    />
  )
}

// A tab: it is the current page when the URL matches (aria-current=page).
export function IndexTabLink({ className, ...props }: Omit<NavLinkProps, 'className'> & { className?: string }) {
  return <NavLink className={({ isActive }) => cn(indexTabClass(isActive), className)} {...props} />
}
