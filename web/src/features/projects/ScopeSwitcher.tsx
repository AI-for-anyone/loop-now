import { useId, useState, type KeyboardEvent } from 'react'
import { useNavigate } from 'react-router'
import { Popover as PopoverPrimitive } from 'radix-ui'
import { CheckIcon, PlusIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { NavIcon } from '@/components/NavIcon'
import { t } from '@/i18n'
import { projectAvatarPalette, projectMonogram } from '@/lib/avatar'
import { GLOBAL, lastScope, overviewPath, PLATFORM, type ResolvedShellScope, type ShellScope } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { useTheme } from '@/theme/store'
import { CreateProjectDialog } from './CreateProjectDialog'

const text = t.shell

type Option = { kind: 'global' } | { kind: 'project'; project: Schemas['Project'] }

// Option row: fixed single-line height with semantic active and hover highlights.
const rowClass = (current: boolean, active: boolean) =>
  cn(
    'grid h-[38px] cursor-pointer grid-cols-[18px_minmax(0,1fr)_max-content] items-center gap-2 rounded-ticket py-0 pr-3 pl-2.5 text-[14.5px] transition-colors',
    current ? 'bg-surface-active font-semibold text-ink' : 'font-medium hover:bg-surface-hover',
    active && !current && 'bg-surface-hover',
  )

// Colored monogram badge for a project: each project gets a stable distinct hue derived from its id/name.
export function ProjectAvatar({
  id,
  name,
  size = 'sm',
  className,
}: {
  id?: string
  name: string
  size?: 'xs' | 'sm'
  className?: string
}) {
  const { resolvedTheme } = useTheme()
  const monogram = projectMonogram(name)
  const palette = projectAvatarPalette(id ?? name)
  const colors = palette[resolvedTheme]
  const sizeClass = size === 'xs' ? 'size-[18px] rounded-[5px] text-[9.5px]' : 'size-[22px] rounded-[6px] text-[10.5px]'

  return (
    <span
      aria-hidden="true"
      data-project-avatar={palette.name}
      data-monogram={monogram}
      style={{ backgroundColor: colors.bg, color: colors.fg }}
      className={cn(
        'inline-flex shrink-0 items-center justify-center font-sans leading-none font-bold tracking-[-0.02em] select-none before:content-[attr(data-monogram)]',
        sizeClass,
        className,
      )}
    />
  )
}

// The small badge on the left of the trigger button: a unique colored monogram avatar for a project, or a subtle globe badge for Global.
function ScopeMark({ scope }: { scope: ResolvedShellScope }) {
  if (scope.kind === 'project') {
    return <ProjectAvatar id={scope.project?.id ?? scope.name} name={scope.name} size="sm" />
  }
  return (
    <span
      aria-hidden="true"
      className="inline-flex size-[22px] shrink-0 items-center justify-center rounded-[6px] bg-sidebar-control-bg text-sidebar-control-fg"
    >
      <NavIcon name="settings" className="size-3.5" />
    </span>
  )
}

// The scope switcher docked in the sidebar footer: collapsed, it is a borderless trigger showing the current scope's colored avatar and name;
// expanded, it lists Global, each project (with its colored monogram avatar and Agent count), New project, and the server connection status.
// Focus stays in the filter box; the up and down arrow keys move between options and Enter opens one; New project is reached with Tab;
// Esc closes the list and returns focus to the trigger button.
export function ScopeSwitcher({
  scope,
  projects,
  side = 'top',
  className,
  statusDotClass,
  statusTitle,
}: {
  scope: ResolvedShellScope
  projects: Schemas['Project'][] | undefined
  side?: 'top' | 'bottom'
  className?: string
  statusDotClass?: string
  statusTitle?: string
}) {
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [filter, setFilter] = useState('')
  const [active, setActive] = useState(0)
  const [creating, setCreating] = useState(false)
  const groupId = useId()
  const listId = useId()

  const currentId = scope.kind === 'project' ? (scope.project?.id ?? null) : 'global'
  const keyword = filter.trim().toLowerCase()
  const shown = (projects ?? []).filter((p) => p.name.toLowerCase().includes(keyword))
  const options: Option[] = [{ kind: 'global' }, ...shown.map((project) => ({ kind: 'project' as const, project }))]
  const activeIndex = active < 0 ? -1 : Math.min(active, options.length - 1)
  const optionKey = (option: Option) => (option.kind === 'global' ? 'global' : option.project.id)
  const optionId = (index: number) => `${listId}-${index}`

  const openChange = (next: boolean) => {
    setOpen(next)
    if (next) {
      setFilter('')
      const allOptions: Option[] = [{ kind: 'global' }, ...(projects ?? []).map((project) => ({ kind: 'project' as const, project }))]
      setActive(Math.max(0, allOptions.findIndex((o) => optionKey(o) === currentId)))
    }
  }

  const choose = (option: Option | undefined) => {
    if (!option) return
    lastScope.set(option.kind === 'global' ? PLATFORM : option.project.id)
    setOpen(false)
    const target: ShellScope = option.kind === 'global' ? GLOBAL : { kind: 'project', name: option.project.name }
    void navigate(overviewPath(target))
  }

  const onFilterChange = (nextValue: string) => {
    setFilter(nextValue)
    const nextKeyword = nextValue.trim().toLowerCase()
    if (nextKeyword === '') {
      setActive(0)
      return
    }
    const nextShown = (projects ?? []).filter((p) => p.name.toLowerCase().includes(nextKeyword))
    if (nextShown.length > 0) {
      // Default to the first matching project so pressing Enter opens it instead of jumping to Global.
      setActive(1)
    } else if (text.global.toLowerCase().includes(nextKeyword)) {
      setActive(0)
    } else {
      setActive(-1)
    }
  }

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      setActive((index) => Math.min(index + 1, options.length - 1))
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      setActive((index) => Math.max(index - 1, 0))
    } else if (event.key === 'Enter') {
      event.preventDefault()
      if (activeIndex >= 0) choose(options[activeIndex])
    }
  }

  const option = (item: Option, index: number) => {
    const isCurrent = optionKey(item) === currentId
    return (
      <li
        key={optionKey(item)}
        id={optionId(index)}
        role="option"
        aria-selected={isCurrent}
        onMouseDown={(event) => event.preventDefault()}
        onMouseEnter={() => setActive(index)}
        onClick={() => choose(item)}
        className={rowClass(isCurrent, index === activeIndex)}
      >
        <span aria-hidden="true" className="inline-flex w-[18px]">
          {isCurrent && <CheckIcon className="size-4" strokeWidth={2.5} />}
        </span>
        <span className="flex min-w-0 items-center gap-2">
          {item.kind === 'project' ? (
            <ProjectAvatar id={item.project.id} name={item.project.name} size="xs" />
          ) : (
            <span aria-hidden="true" className="inline-flex size-[18px] shrink-0 items-center justify-center rounded-[5px] bg-surface-subtle text-ink-500">
              <NavIcon name="settings" className="size-3" />
            </span>
          )}
          <span title={item.kind === 'project' ? item.project.name : undefined} className="truncate">
            {item.kind === 'global' ? text.global : item.project.name}
          </span>
          {item.kind === 'project' && item.project.isDefault && (
            <span className="shrink-0 rounded-stamp border border-hairline-strong bg-surface-subtle px-1.5 text-[11px] leading-5 font-semibold text-ink-700">
              {t.projects.switcher.isDefault}
            </span>
          )}
        </span>
        {item.kind === 'project' ? (
          <span className={cn('text-[13px] font-medium whitespace-nowrap', isCurrent ? 'text-ink-700' : 'text-ink-500')}>
            {t.projects.switcher.agentCount(item.project.agentCount)}
          </span>
        ) : (
          <span />
        )}
      </li>
    )
  }

  const name =
    scope.kind === 'global' ? text.global : scope.project ? scope.project.name : projects ? t.projects.overview.missingOption(scope.name) : scope.name

  return (
    <>
      <PopoverPrimitive.Root open={open} onOpenChange={openChange}>
        <PopoverPrimitive.Trigger
          type="button"
          aria-haspopup="listbox"
          aria-label={text.scope.label(name)}
          title={name}
          className={cn(
            'flex h-9 min-w-0 items-center gap-2 rounded-ticket border-0 bg-transparent px-1.5 text-[14px] text-sidebar-fg transition-colors outline-none hover:bg-sidebar-hover focus-visible:shadow-focus',
            open && 'bg-sidebar-hover',
            className,
          )}
        >
          <ScopeMark scope={scope} />
          <span className="min-w-0 flex-1 truncate text-left font-semibold">{name}</span>
          <NavIcon name="updown" className="ml-auto size-3.5 shrink-0 text-sidebar-muted" />
        </PopoverPrimitive.Trigger>
        <PopoverPrimitive.Portal>
          <PopoverPrimitive.Content
            side={side}
            align="start"
            sideOffset={8}
            collisionPadding={16}
            aria-label={text.scope.list}
            className="z-50 flex w-[min(320px,calc(100vw-32px))] flex-col gap-1.5 rounded-card border border-hairline-strong bg-surface-elevated p-2 text-ink shadow-floating outline-none"
            onOpenAutoFocus={(event) => {
              event.preventDefault()
              ;(event.currentTarget as HTMLElement).querySelector('input')?.focus()
            }}
          >
            <input
              type="search"
              aria-label={text.scope.filter}
              placeholder={text.scope.filter}
              aria-controls={listId}
              aria-activedescendant={activeIndex >= 0 && options[activeIndex] ? optionId(activeIndex) : undefined}
              value={filter}
              onChange={(event) => onFilterChange(event.target.value)}
              onKeyDown={onKeyDown}
              className="h-[36px] w-full rounded-input border border-hairline-strong bg-surface-control px-3 text-[14.5px] text-ink transition-colors outline-none placeholder:text-ink-500 focus-visible:border-ink/35"
            />
            <div id={listId} role="listbox" aria-label={text.scope.list} className="flex flex-col gap-0.5">
              <ul role="none" className="flex flex-col">
                {option(options[0], 0)}
              </ul>
              <span aria-hidden="true" className="mx-1.5 my-1 border-t border-hairline" />
              <span id={groupId} className="px-3 pt-0.5 text-[12.5px] font-semibold text-ink-500">
                {text.scope.projects(shown.length)}
              </span>
              {/* Show at most 7 rows and scroll inside the list beyond that; on narrow screens at most half the screen height. */}
              <ul role="group" aria-labelledby={groupId} className="flex max-h-[min(280px,50vh)] flex-col gap-0.5 overflow-y-auto">
                {shown.length === 0 && (
                  <li role="presentation" className="px-3 py-2 text-[14px] text-ink-500">
                    {text.scope.noMatch}
                  </li>
                )}
                {options.slice(1).map((item, index) => option(item, index + 1))}
              </ul>
            </div>
            <span aria-hidden="true" className="mx-1.5 my-1 border-t border-hairline" />
            <button
              type="button"
              className="flex h-[38px] items-center gap-2.5 rounded-ticket px-3 text-[14.5px] font-semibold whitespace-nowrap text-ink transition-colors outline-none hover:bg-surface-hover focus-visible:shadow-focus"
              onClick={() => {
                setOpen(false)
                setCreating(true)
              }}
            >
              <PlusIcon className="size-4" strokeWidth={2.5} />
              {text.scope.create}
            </button>
            {statusDotClass && statusTitle && (
              <div
                data-scope-connection-status
                className="flex items-center gap-2 border-t border-hairline px-3 pt-2 pb-0.5 text-[11.5px] text-ink-500"
              >
                <span aria-hidden="true" className={cn('size-2 shrink-0 rounded-full', statusDotClass)} />
                <span className="min-w-0 truncate">{statusTitle}</span>
              </div>
            )}
          </PopoverPrimitive.Content>
        </PopoverPrimitive.Portal>
      </PopoverPrimitive.Root>
      {creating && <CreateProjectDialog onClose={() => setCreating(false)} />}
    </>
  )
}
