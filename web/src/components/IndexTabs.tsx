import { useRef, type ComponentProps, type KeyboardEvent, type ReactNode } from 'react'
import { NavLink, type NavLinkProps } from 'react-router'
import { indexTabClass, indexTabsClass } from '@/lib/index-tab'
import { cn } from '@/lib/utils'

// A row of index tabs sitting right below the page header. Scrolls horizontally when narrow without widening the page.
export function IndexTabs({ className, ...props }: ComponentProps<'nav'>) {
  return (
    <nav
      className={cn(indexTabsClass, className)}
      {...props}
    />
  )
}

// A route-based tab link: marked with aria-current="page" when the URL matches.
export function IndexTabLink({ className, ...props }: Omit<NavLinkProps, 'className'> & { className?: string }) {
  return <NavLink className={({ isActive }) => cn(indexTabClass(isActive), className)} {...props} />
}

// A controlled tab button for manual tablist compositions.
export function SubNavTab({
  active,
  className,
  ...props
}: { active: boolean } & ComponentProps<'button'>) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      tabIndex={active ? 0 : -1}
      className={cn(indexTabClass(active), 'cursor-pointer disabled:pointer-events-none disabled:opacity-45', className)}
      {...props}
    />
  )
}

export interface SubNavLinkItem {
  key: string
  label: ReactNode
  to: string
  end?: boolean
  count?: number | string
  badge?: ReactNode
}

export interface SubNavTabItem<K extends string = string> {
  key: K
  label: ReactNode
  count?: number | string
  badge?: ReactNode
  disabled?: boolean
  controls?: string
}

export type SubNavProps<K extends string = string> =
  | {
      'aria-label': string
      items: readonly SubNavLinkItem[]
      value?: never
      onChange?: never
      className?: string
    }
  | {
      'aria-label': string
      items: readonly SubNavTabItem<K>[]
      value: K
      onChange: (key: K) => void
      className?: string
    }

function SubNavItemMeta({ count, badge }: { count?: number | string; badge?: ReactNode }) {
  return (
    <>
      {count !== undefined && (
        <span className="font-mono text-[11px] font-medium text-ink-500 tabular-nums">
          {count}
        </span>
      )}
      {badge}
    </>
  )
}

// Unified flush sub-navigation bar: supports both route links (nav + NavLink) and controlled tabs (tablist + tab with keyboard arrows).
export function SubNav<K extends string = string>(props: SubNavProps<K>) {
  const { 'aria-label': ariaLabel, className } = props
  const tabRefs = useRef<Map<string, HTMLButtonElement>>(new Map())

  if (props.onChange === undefined) {
    return (
      <nav aria-label={ariaLabel} className={cn(indexTabsClass, className)}>
        {props.items.map((item) => (
          <NavLink
            key={item.key}
            to={item.to}
            end={item.end}
            className={({ isActive }) => indexTabClass(isActive)}
          >
            <span>{item.label}</span>
            <SubNavItemMeta count={item.count} badge={item.badge} />
          </NavLink>
        ))}
      </nav>
    )
  }

  const { items, value, onChange } = props
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== 'ArrowRight' && event.key !== 'ArrowLeft' && event.key !== 'Home' && event.key !== 'End') return
    const enabled = items.filter((item) => !item.disabled)
    if (enabled.length === 0) return
    event.preventDefault()
    const currentIndex = Math.max(0, enabled.findIndex((item) => item.key === value))
    const nextIndex =
      event.key === 'Home'
        ? 0
        : event.key === 'End'
          ? enabled.length - 1
          : event.key === 'ArrowRight'
            ? (currentIndex + 1) % enabled.length
            : (currentIndex - 1 + enabled.length) % enabled.length
    const nextItem = enabled[nextIndex]
    if (!nextItem) return
    onChange(nextItem.key)
    tabRefs.current.get(nextItem.key)?.focus()
  }

  return (
    <div
      role="tablist"
      aria-label={ariaLabel}
      onKeyDown={onKeyDown}
      className={cn(indexTabsClass, className)}
    >
      {items.map((item) => {
        const active = item.key === value
        return (
          <SubNavTab
            key={item.key}
            ref={(node) => {
              if (node) tabRefs.current.set(item.key, node)
              else tabRefs.current.delete(item.key)
            }}
            active={active}
            aria-controls={item.controls}
            disabled={item.disabled}
            onClick={() => onChange(item.key)}
          >
            <span>{item.label}</span>
            <SubNavItemMeta count={item.count} badge={item.badge} />
          </SubNavTab>
        )
      })}
    </div>
  )
}
