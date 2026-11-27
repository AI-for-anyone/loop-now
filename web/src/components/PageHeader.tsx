import { Fragment, type ComponentProps, type ReactNode } from 'react'
import { Link } from 'react-router'
import { PanelLeftOpen } from 'lucide-react'
import { useSidebarControl } from '@/app/sidebar-context'
import { mac } from '@/features/chat/composer-style'
import { hasInsetTitleBar } from '@/features/desktop/desktop'
import { t } from '@/i18n'
import { sheetTitleClass } from '@/lib/page-title'
import { cn } from '@/lib/utils'

export function SidebarExpandButton() {
  const { collapsed, expand } = useSidebarControl()
  if (!collapsed) return null
  return (
    <button
      type="button"
      aria-label={t.shell.expandNav}
      title={`${t.shell.expandNav} (${t.shell.sidebarShortcut(mac)})`}
      onClick={expand}
      className="window-no-drag inline-flex size-7 shrink-0 items-center justify-center rounded-ticket text-ink-500 transition-colors outline-none hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus max-nav:hidden"
    >
      <PanelLeftOpen className="size-4" strokeWidth={1.8} />
    </button>
  )
}

export interface Crumb {
  label: string
  // A level without a URL shows text only (e.g. the scope itself on the overview page).
  to?: string
}

// Page frame: full-bleed column directly filling the main area without outer margins.
export function PageFrame({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('flex h-full min-h-0 flex-col', className)} {...props} />
}

// Page header: single-row 48px bar with inline breadcrumb trail, title, meta info and right-aligned actions
// (plus an optional slim secondary band for `note`).
export function PageHeader({
  crumbs,
  title,
  current = title,
  lead,
  actions,
  note,
  children,
  className,
}: {
  crumbs: Crumb[]
  title: string
  current?: string
  lead?: ReactNode
  actions?: ReactNode
  note?: ReactNode
  children?: ReactNode
  className?: string
}) {
  const heading = (
    <h1 className={cn(sheetTitleClass, 'truncate', lead && 'min-w-0')}>
      {title}
    </h1>
  )
  const desktop = hasInsetTitleBar()
  const { collapsed } = useSidebarControl()
  return (
    <header data-page-header className={cn('@container relative flex shrink-0 flex-col', className)}>
      <div
        data-page-bar
        className={cn(
          'relative flex min-h-12 flex-wrap items-center gap-y-1.5 border-b border-hairline bg-surface-canvas px-4 py-1.5 sm:px-6 nav:min-h-12 nav:py-1',
          (crumbs.length > 0 || collapsed) && 'gap-x-1.5',
          desktop && 'window-drag',
          collapsed && desktop && 'nav:pl-[84px]',
        )}
      >
        <div className="flex min-w-0 items-center gap-2">
          <SidebarExpandButton />
          <nav aria-label={t.shell.breadcrumb} className="min-w-0 shrink-0">
            <ol className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-[13.5px] font-medium text-ink-500 nav:leading-4">
              {crumbs.map((crumb, index) => (
                <Fragment key={index}>
                  <li className="min-w-0 shrink-0 [overflow-wrap:anywhere]">
                    {crumb.to ? (
                      <Link to={crumb.to} className="hover:text-ink hover:underline">
                        {crumb.label}
                      </Link>
                    ) : (
                      crumb.label
                    )}
                  </li>
                  <li aria-hidden="true" className="shrink-0 text-ink-500/70">
                    ›
                  </li>
                </Fragment>
              ))}
              <li aria-current="page" className="sr-only text-ink">
                {current}
              </li>
            </ol>
          </nav>
        </div>
        <div data-page-title-row className="relative flex min-w-0 flex-1 flex-wrap items-center gap-x-3 gap-y-1.5">
          {lead ? (
            <div data-page-title-lead className="flex min-w-0 items-center gap-2">
              {lead}
              {heading}
            </div>
          ) : (
            heading
          )}
          {children && (
            <div data-page-meta className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1.5 text-[13px] text-ink-500">
              {children}
            </div>
          )}
          {actions && (
            <div data-page-actions className="ml-auto flex shrink-0 flex-wrap items-center gap-2">
              {actions}
            </div>
          )}
        </div>
      </div>
      {note && (
        <div
          data-page-note
          className="relative border-b border-hairline bg-surface-subtle/50 px-4 py-2 text-[13.5px] text-ink-700 [overflow-wrap:anywhere] sm:px-6"
        >
          {note}
        </div>
      )}
    </header>
  )
}

// Live status in the header's meta info: a green dot plus a label when connected, a red dot plus "reconnecting" when disconnected;
// while still connecting it only holds the space, without a flash of red.
export function LiveDot({ state, label }: { state: 'connecting' | 'open' | 'reconnecting' | 'closed'; label: string }) {
  if (state === 'connecting') return <span role="status" aria-live="polite" />
  const open = state === 'open'
  return (
    <span
      role="status"
      aria-live="polite"
      className={cn('inline-flex items-center gap-1.5 text-[13px] font-medium', open ? 'text-green-text' : 'text-red-text')}
    >
      <span aria-hidden="true" className={cn('size-[7px] shrink-0 rounded-full', open ? 'bg-green' : 'bg-red')} />
      {open ? label : t.chat.connection.reconnecting}
    </span>
  )
}
