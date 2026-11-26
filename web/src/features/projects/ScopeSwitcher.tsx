import { useId, useState, type KeyboardEvent } from 'react'
import { useNavigate } from 'react-router'
import { Popover as PopoverPrimitive } from 'radix-ui'
import { CheckIcon, PlusIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { NavIcon } from '@/components/NavIcon'
import { t } from '@/i18n'
import { GLOBAL, lastScope, overviewPath, PLATFORM, type ResolvedShellScope, type ShellScope } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { CreateProjectDialog } from './CreateProjectDialog'

const text = t.shell

type Option = { kind: 'global' } | { kind: 'project'; project: Schemas['Project'] }

// Option row: semantic active/hover highlights in mono; yellow current row and navy inner outline in Plate.
const rowClass = (current: boolean, active: boolean) =>
  cn(
    'grid min-h-[36px] cursor-pointer grid-cols-[18px_minmax(0,1fr)_max-content] items-center gap-2 rounded-ticket py-0 pr-3 pl-2.5 text-[13.5px] transition-colors [[data-skin=plate]_&]:min-h-[38px] [[data-skin=plate]_&]:text-[14px]',
    current
      ? 'bg-surface-active font-semibold text-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:font-black'
      : 'font-medium hover:bg-surface-hover [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:hover:bg-tint-blue',
    active &&
      (current
        ? 'ring-1 ring-ink/50 ring-inset [[data-skin=plate]_&]:ring-0 [[data-skin=plate]_&]:shadow-[inset_0_0_0_2px_var(--ink)]'
        : 'bg-surface-hover ring-1 ring-ink/50 ring-inset [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:ring-0 [[data-skin=plate]_&]:shadow-[inset_0_0_0_2px_var(--ink)]'),
  )

// The small badge on the left of the trigger button: the first character of the name for a project, a globe icon for Global
// (NavIcon "settings", which draws a globe).
function ScopeMark({ scope }: { scope: ResolvedShellScope }) {
  return (
    <span
      aria-hidden="true"
      className="inline-flex size-[24px] shrink-0 items-center justify-center rounded-stamp border border-hairline-strong bg-surface-subtle font-display-latin text-[12px] leading-none font-semibold text-ink uppercase [[data-skin=plate]_&]:size-[26px] [[data-skin=plate]_&]:rounded-[8px] [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:text-[13px] [[data-skin=plate]_&]:font-extrabold"
    >
      {scope.kind === 'project' ? Array.from(scope.name)[0] : <NavIcon name="settings" className="size-3.5" />}
    </span>
  )
}

// The scope switcher at the top of the sidebar: collapsed, it is a trigger showing the current scope; expanded, it lists
// “全局” (Global), each project (with its Agent count) and “新建项目” (New project), in that order.
// Focus stays in the filter box; the up and down arrow keys move between options and Enter opens one; “新建项目” (New project)
// is reached with Tab; Esc closes the list and returns focus to the trigger button.
// Choosing a scope opens its overview and remembers it (legacy URLs without a project redirect based on it).
export function ScopeSwitcher({ scope, projects }: { scope: ResolvedShellScope; projects: Schemas['Project'][] | undefined }) {
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
          <span className="truncate">{item.kind === 'global' ? text.global : item.project.name}</span>
          {item.kind === 'project' && item.project.isDefault && (
            <span className="shrink-0 rounded-stamp border border-hairline-strong bg-surface-subtle px-1.5 text-[11px] leading-5 font-semibold text-ink-700 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-white [[data-skin=plate]_&]:px-[7px] [[data-skin=plate]_&]:text-[12px] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:shadow-[inset_0_0_0_1.5px_var(--ink)]">
              {t.projects.switcher.isDefault}
            </span>
          )}
        </span>
        {item.kind === 'project' ? (
          <span className={cn('text-[12px] font-medium whitespace-nowrap [[data-skin=plate]_&]:text-[12.5px] [[data-skin=plate]_&]:font-bold', isCurrent ? 'text-ink-700' : 'text-ink-500')}>
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
            'flex min-h-10 items-center gap-2.5 rounded-card border border-hairline-strong bg-surface-control px-2.5 text-[14px] text-ink transition-colors outline-none hover:bg-surface-hover focus-visible:shadow-focus [[data-skin=plate]_&]:mr-[3px] [[data-skin=plate]_&]:min-h-12 [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:text-[14.5px] [[data-skin=plate]_&]:shadow-[4px_4px_0_var(--ink)]',
            open && 'border-ink bg-surface-hover [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:shadow-[4px_4px_0_var(--ink),0_0_0_3px_var(--riso-yellow)]',
          )}
        >
          <ScopeMark scope={scope} />
          <span className="min-w-0 truncate font-semibold [[data-skin=plate]_&]:font-black">{name}</span>
          <NavIcon name="updown" className="ml-auto size-3.5 shrink-0 text-ink-500 [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:[stroke-width:2.8]" />
        </PopoverPrimitive.Trigger>
        <PopoverPrimitive.Portal>
          <PopoverPrimitive.Content
            align="start"
            sideOffset={8}
            collisionPadding={16}
            aria-label={text.scope.list}
            className="z-50 flex w-[min(320px,calc(100vw-32px))] flex-col gap-1.5 rounded-card border border-hairline-strong bg-surface-elevated p-2 text-ink shadow-floating outline-none [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:shadow-[0_0_0_2px_var(--ink),4px_4px_0_2px_var(--riso-blue)]"
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
              className="h-[36px] w-full rounded-input border border-hairline-strong bg-surface-control px-3 text-[13.5px] text-ink outline-none placeholder:text-ink-500 focus-visible:border-ink [[data-skin=plate]_&]:h-[38px] [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue [[data-skin=plate]_&]:px-3.5 [[data-skin=plate]_&]:shadow-[inset_0_0_0_2px_var(--ink)]"
            />
            <div id={listId} role="listbox" aria-label={text.scope.list} className="flex flex-col gap-0.5">
              <ul role="none" className="flex flex-col">
                {option(options[0], 0)}
              </ul>
              <span aria-hidden="true" className="mx-1.5 my-1 border-t border-hairline [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/18" />
              <span id={groupId} className="px-3 pt-0.5 text-[11.5px] font-semibold text-ink-500 [[data-skin=plate]_&]:text-[12px] [[data-skin=plate]_&]:font-black">
                {text.scope.projects(shown.length)}
              </span>
              {/* Show at most 7 rows and scroll inside the list beyond that; on narrow screens at most half the screen height. */}
              <ul role="group" aria-labelledby={groupId} className="flex max-h-[min(280px,50vh)] flex-col gap-0.5 overflow-y-auto">
                {shown.length === 0 && (
                  <li role="presentation" className="px-3 py-2 text-[13.5px] text-ink-500">
                    {text.scope.noMatch}
                  </li>
                )}
                {options.slice(1).map((item, index) => option(item, index + 1))}
              </ul>
            </div>
            <span aria-hidden="true" className="mx-1.5 my-1 border-t border-hairline [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/18" />
            <button
              type="button"
              className="flex min-h-[36px] items-center gap-2.5 rounded-ticket px-3 text-[13.5px] font-semibold text-ink transition-colors outline-none hover:bg-surface-hover focus-visible:shadow-focus [[data-skin=plate]_&]:min-h-[38px] [[data-skin=plate]_&]:text-[14px] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:hover:bg-tint-blue"
              onClick={() => {
                setOpen(false)
                setCreating(true)
              }}
            >
              <PlusIcon className="size-4" strokeWidth={2.5} />
              {text.scope.create}
            </button>
          </PopoverPrimitive.Content>
        </PopoverPrimitive.Portal>
      </PopoverPrimitive.Root>
      {creating && <CreateProjectDialog onClose={() => setCreating(false)} />}
    </>
  )
}
