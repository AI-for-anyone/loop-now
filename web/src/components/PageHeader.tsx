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

// Page frame: the header on top, the full-bleed sheet directly below, meeting the sidebar and window edges without outer margins.
export function PageFrame({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('flex h-full min-h-0 flex-col', className)} {...props} />
}

// Page title (rule 3 of the redesign's general rules): breadcrumbs, a ZCOOL title (riso blue with a misregistered fluorescent pink copy),
// a line of meta info, and an overprint decoration in the top right corner.
// Used for project and global pages (overview, tasks, project settings, project folder…); Agent details have their own header (name,
// badges, address).
// The last breadcrumb is the current page, i.e. the title itself. The header is a size container: wider than 820px it shows the
// decoration, and every line of text makes room for it.
// On wide screens it is as compact as the Agent header: a 40px title, the meta info moved onto the title's line to its right (wrapping
// when it does not fit), and the decoration shrunk to the header's height; narrow screens are unchanged.
// current: how the last breadcrumb is written (the title by default; task details write “#11”); lead: something before the title (such
// as the task number) that is not counted as part of the title;
// actions: actions on the right of this line (such as “取消任务” (Cancel task)); note: a line of explanation under the title (such as a
// plugin's summary and source).
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
  const heading = <h1 className={cn(sheetTitleClass, 'self-start nav:self-auto nav:text-[40px] nav:leading-none nav:[text-shadow:3px_3px_0_var(--riso-pink)]', lead && 'min-w-0 [overflow-wrap:anywhere]')}>{title}</h1>
  return (
    <header data-page-header className={cn('@container relative flex shrink-0 flex-col gap-3 px-3.5 pt-[22px] pb-[18px] nav:gap-1.5 nav:px-6 nav:pt-3 nav:pb-2.5', className)}>
      <OverprintDeco compact />
      <nav aria-label={t.shell.breadcrumb} className="relative @[820px]:pr-[316px] nav:@[820px]:pr-[196px]">
        <ol className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-[12px] font-bold text-ink-500 nav:leading-4">
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
          <li aria-current="page" className="min-w-0 [overflow-wrap:anywhere] text-riso-blue">
            {current}
          </li>
        </ol>
      </nav>
      <div
        data-page-title-row
        className="relative flex min-w-0 flex-col gap-3 @[820px]:pr-[316px] nav:flex-row nav:flex-wrap nav:items-center nav:gap-x-4 nav:gap-y-1.5 nav:@[820px]:pr-[196px]"
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
      {note && <div data-page-note className="relative text-[13px] text-ink-700 [overflow-wrap:anywhere] @[820px]:pr-[316px] nav:@[820px]:pr-[196px]">{note}</div>}
    </header>
  )
}

// Live status in the header's meta info: a green dot plus a label when connected (such as “实时更新中” (Live)), a red dot plus
// "reconnecting" when disconnected; while still connecting it only holds the space, without a flash of red.
export function LiveDot({ state, label }: { state: 'connecting' | 'open' | 'reconnecting' | 'closed'; label: string }) {
  if (state === 'connecting') return <span role="status" aria-live="polite" />
  const open = state === 'open'
  return (
    <span role="status" aria-live="polite" className={cn('inline-flex items-center gap-1.5 text-[12px] font-black', open ? 'text-green-text' : 'text-red-text')}>
      <span aria-hidden="true" className={cn('size-[7px] shrink-0 rounded-full', open ? 'bg-green' : 'bg-red')} />
      {open ? label : t.chat.connection.reconnecting}
    </span>
  )
}
