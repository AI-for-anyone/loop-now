import { Fragment, type ComponentProps, type ReactNode } from 'react'
import { Link } from 'react-router'
import { PanelLeftOpen } from 'lucide-react'
import { useSidebarControl } from '@/app/sidebar-context'
import { OverprintDeco } from '@/components/OverprintDeco'
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
      title={`${t.shell.expandNav} (⌘B)`}
      onClick={expand}
      className="window-no-drag inline-flex size-7 shrink-0 items-center justify-center rounded-ticket text-ink-500 transition-colors outline-none hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus max-nav:hidden [[data-skin=plate]_&]:size-8 [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:shadow-[2px_2px_0_var(--ink)]"
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

// Page frame: full-bleed column in mono skin (zero outer horizontal or bottom padding); Plate skin restores fluid side padding and
// bottom room for the sheet's offset shadow.
export function PageFrame({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      className={cn(
        'flex h-full min-h-0 flex-col p-0 [[data-skin=plate]_&]:px-[clamp(14px,2.4vw,34px)] [[data-skin=plate]_&]:pb-[26px] [[data-skin=plate]_&]:max-nav:pb-4',
        className,
      )}
      {...props}
    />
  )
}

// Page header: single-row 48px bar in mono skin with inline breadcrumb trail, title, meta info and right-aligned actions
// (plus an optional slim secondary band for `note`).
// Plate skin restores the multi-row layout with OverprintDeco, ZCOOL display title, misregistered pink shadow and padding offsets.
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
    <h1
      className={cn(
        sheetTitleClass,
        'truncate [[data-skin=plate]_&]:self-start [[data-skin=plate]_&]:overflow-visible [[data-skin=plate]_&]:text-clip [[data-skin=plate]_&]:whitespace-normal [[data-skin=plate]_&]:[overflow-wrap:anywhere] [[data-skin=plate]_&]:nav:self-auto [[data-skin=plate]_&]:nav:text-[40px] [[data-skin=plate]_&]:nav:leading-none [[data-skin=plate]_&]:nav:[text-shadow:3px_3px_0_var(--riso-pink)]',
        lead && 'min-w-0',
      )}
    >
      {title}
    </h1>
  )
  const desktop = hasInsetTitleBar()
  const { collapsed } = useSidebarControl()
  return (
    <header
      data-page-header
      className={cn(
        '@container relative flex shrink-0 flex-col [[data-skin=plate]_&]:gap-3 [[data-skin=plate]_&]:pt-[22px] [[data-skin=plate]_&]:pb-[18px] [[data-skin=plate]_&]:nav:gap-1 [[data-skin=plate]_&]:nav:pt-0 [[data-skin=plate]_&]:nav:pb-2.5',
        className,
      )}
    >
      <OverprintDeco compact />
      <div
        data-page-bar
        className={cn(
          'relative flex min-h-12 flex-wrap items-center gap-y-1.5 border-b border-hairline bg-surface-canvas px-4 py-1.5 sm:px-6 nav:min-h-12 nav:py-1',
          (crumbs.length > 0 || collapsed) && 'gap-x-1.5',
          desktop && 'window-drag',
          collapsed && desktop && 'nav:pl-[84px]',
          '[[data-skin=plate]_&]:min-h-0 [[data-skin=plate]_&]:flex-col [[data-skin=plate]_&]:items-stretch [[data-skin=plate]_&]:gap-3 [[data-skin=plate]_&]:border-b-0 [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:p-0 [[data-skin=plate]_&]:nav:gap-1 [[data-skin=plate]_&]:nav:pl-0',
        )}
      >
        <div
          className={cn(
            'flex min-w-0 items-center gap-2 [[data-skin=plate]_&]:w-full [[data-skin=plate]_&]:nav:h-12 [[data-skin=plate]_&]:@[820px]:pr-[316px] [[data-skin=plate]_&]:nav:@[820px]:pr-[196px]',
            collapsed && desktop && '[[data-skin=plate]_&]:nav:pl-[calc(84px-clamp(14px,2.4vw,34px))]',
          )}
        >
          <SidebarExpandButton />
          <nav aria-label={t.shell.breadcrumb} className="min-w-0 shrink-0 [[data-skin=plate]_&]:flex-1">
            <ol className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-[12.5px] font-medium text-ink-500 nav:leading-4 [[data-skin=plate]_&]:text-[12px] [[data-skin=plate]_&]:font-bold">
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
                  <li aria-hidden="true" className="shrink-0 text-ink-500/70 [[data-skin=plate]_&]:text-ink-500">
                    ›
                  </li>
                </Fragment>
              ))}
              <li
                aria-current="page"
                className="sr-only text-ink [[data-skin=plate]_&]:not-sr-only [[data-skin=plate]_&]:min-w-0 [[data-skin=plate]_&]:[overflow-wrap:anywhere] [[data-skin=plate]_&]:text-riso-blue"
              >
                {current}
              </li>
            </ol>
          </nav>
        </div>
        <div
          data-page-title-row
          className="relative flex min-w-0 flex-1 flex-wrap items-center gap-x-3 gap-y-1.5 [[data-skin=plate]_&]:flex-col [[data-skin=plate]_&]:items-stretch [[data-skin=plate]_&]:gap-3 [[data-skin=plate]_&]:[-webkit-app-region:no-drag] [[data-skin=plate]_&]:nav:flex-row [[data-skin=plate]_&]:nav:flex-wrap [[data-skin=plate]_&]:nav:items-center [[data-skin=plate]_&]:nav:gap-x-4 [[data-skin=plate]_&]:nav:gap-y-1.5 [[data-skin=plate]_&]:@[820px]:pr-[316px] [[data-skin=plate]_&]:nav:@[820px]:pr-[196px]"
        >
          {lead ? (
            <div
              data-page-title-lead
              className="flex min-w-0 items-center gap-2 [[data-skin=plate]_&]:items-baseline [[data-skin=plate]_&]:gap-3.5 [[data-skin=plate]_&]:self-start [[data-skin=plate]_&]:nav:self-auto"
            >
              {lead}
              {heading}
            </div>
          ) : (
            heading
          )}
          {children && (
            <div
              data-page-meta
              className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1.5 text-[12px] text-ink-500 [[data-skin=plate]_&]:min-h-[26px] [[data-skin=plate]_&]:gap-x-3.5 [[data-skin=plate]_&]:gap-y-2 [[data-skin=plate]_&]:text-[12.5px] [[data-skin=plate]_&]:text-ink-700"
            >
              {children}
            </div>
          )}
          {actions && (
            <div
              data-page-actions
              className="ml-auto flex shrink-0 flex-wrap items-center gap-2 [[data-skin=plate]_&]:ml-0 [[data-skin=plate]_&]:gap-2.5 [[data-skin=plate]_&]:nav:ml-auto"
            >
              {actions}
            </div>
          )}
        </div>
      </div>
      {note && (
        <div
          data-page-note
          className="relative border-b border-hairline bg-surface-subtle/50 px-4 py-2 text-[12.5px] text-ink-700 [overflow-wrap:anywhere] sm:px-6 [[data-skin=plate]_&]:border-b-0 [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:p-0 [[data-skin=plate]_&]:text-[13px] [[data-skin=plate]_&]:@[820px]:pr-[316px] [[data-skin=plate]_&]:nav:@[820px]:pr-[196px]"
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
      className={cn('inline-flex items-center gap-1.5 text-[12px] font-medium [[data-skin=plate]_&]:font-black', open ? 'text-green-text' : 'text-red-text')}
    >
      <span aria-hidden="true" className={cn('size-[7px] shrink-0 rounded-full', open ? 'bg-green' : 'bg-red')} />
      {open ? label : t.chat.connection.reconnecting}
    </span>
  )
}
