import { Fragment, type ComponentProps, type ReactNode } from 'react'
import { Link } from 'react-router'
import { OverprintDeco } from '@/components/OverprintDeco'
import { t } from '@/i18n'
import { sheetTitleClass } from '@/lib/page-title'
import { cn } from '@/lib/utils'

export interface Crumb {
  label: string
  // A level without a URL shows text only (e.g. the scope itself on the overview page).
  to?: string
}

// Page frame: the header on top, the sheet below. The side padding follows the window width; Plate skin leaves extra bottom room for the
// sheet's offset shadow.
export function PageFrame({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      className={cn(
        'flex h-full min-h-0 flex-col px-[clamp(14px,2.4vw,34px)] pb-4 [[data-skin=plate]_&]:pb-[26px] [[data-skin=plate]_&]:max-nav:pb-4',
        className,
      )}
      {...props}
    />
  )
}

// Page header: breadcrumbs, title, meta info and right-aligned actions.
// Compact full-width layout in mono skin (OverprintDeco is mounted and hidden by skin CSS, without reserving right-hand padding);
// Plate skin restores the ZCOOL display title, misregistered pink shadow and overprint decoration padding offsets.
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
        'self-start nav:self-auto [[data-skin=plate]_&]:nav:text-[40px] [[data-skin=plate]_&]:nav:leading-none [[data-skin=plate]_&]:nav:[text-shadow:3px_3px_0_var(--riso-pink)]',
        lead && 'min-w-0 [overflow-wrap:anywhere]',
      )}
    >
      {title}
    </h1>
  )
  return (
    <header
      data-page-header
      className={cn(
        '@container relative flex shrink-0 flex-col gap-1.5 pt-4 pb-3 nav:gap-1.5 nav:pt-3.5 nav:pb-3 [[data-skin=plate]_&]:gap-3 [[data-skin=plate]_&]:pt-[22px] [[data-skin=plate]_&]:pb-[18px] [[data-skin=plate]_&]:nav:gap-1.5 [[data-skin=plate]_&]:nav:pt-3 [[data-skin=plate]_&]:nav:pb-2.5',
        className,
      )}
    >
      <OverprintDeco compact />
      <nav aria-label={t.shell.breadcrumb} className="relative [[data-skin=plate]_&]:@[820px]:pr-[316px] [[data-skin=plate]_&]:nav:@[820px]:pr-[196px]">
        <ol className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-[12px] font-medium text-ink-500 nav:leading-4 [[data-skin=plate]_&]:font-bold">
          {crumbs.map((crumb, index) => (
            <Fragment key={index}>
              <li className="min-w-0 [overflow-wrap:anywhere]">
                {crumb.to ? (
                  <Link to={crumb.to} className="hover:text-ink hover:underline">
                    {crumb.label}
                  </Link>
                ) : (
                  crumb.label
                )}
              </li>
              <li aria-hidden="true">›</li>
            </Fragment>
          ))}
          <li aria-current="page" className="min-w-0 [overflow-wrap:anywhere] text-ink [[data-skin=plate]_&]:text-riso-blue">
            {current}
          </li>
        </ol>
      </nav>
      <div
        data-page-title-row
        className="relative flex min-w-0 flex-col gap-2 nav:flex-row nav:flex-wrap nav:items-center nav:gap-x-4 nav:gap-y-1.5 [[data-skin=plate]_&]:gap-3 [[data-skin=plate]_&]:@[820px]:pr-[316px] [[data-skin=plate]_&]:nav:@[820px]:pr-[196px]"
      >
        {lead ? (
          <div data-page-title-lead className="flex min-w-0 items-baseline gap-3.5 self-start nav:self-auto">
            {lead}
            {heading}
          </div>
        ) : (
          heading
        )}
        {children && (
          <div data-page-meta className="flex min-h-[26px] min-w-0 flex-wrap items-center gap-x-3.5 gap-y-2 text-[12.5px] text-ink-700">
            {children}
          </div>
        )}
        {actions && <div data-page-actions className="flex flex-wrap items-center gap-2.5 nav:ml-auto">{actions}</div>}
      </div>
      {note && (
        <div
          data-page-note
          className="relative text-[13px] text-ink-700 [overflow-wrap:anywhere] [[data-skin=plate]_&]:@[820px]:pr-[316px] [[data-skin=plate]_&]:nav:@[820px]:pr-[196px]"
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
