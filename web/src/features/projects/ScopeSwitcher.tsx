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

// 选项行：当前项打勾、黄底；键盘高亮为 2px 藏青内描边；悬停为浅蓝。
const rowClass = (current: boolean, active: boolean) =>
  cn(
    'grid min-h-[38px] cursor-pointer grid-cols-[18px_minmax(0,1fr)_max-content] items-center gap-2 rounded-ticket py-0 pr-3 pl-2.5 text-[14px]',
    current ? 'bg-riso-yellow font-black' : 'font-bold hover:bg-tint-blue',
    active && 'shadow-[inset_0_0_0_2px_var(--ink)]',
  )

// 触发按钮左侧的黄色小方块：项目取名称的第一个字，全局是地球图标。
function ScopeMark({ scope }: { scope: ResolvedShellScope }) {
  return (
    <span aria-hidden="true" className="inline-flex size-[22px] shrink-0 items-center justify-center rounded-[7px] bg-riso-yellow font-display-latin text-[12px] font-extrabold uppercase shadow-[inset_0_0_0_1.5px_var(--ink)]">
      {scope.kind === 'project' ? Array.from(scope.name)[0] : <NavIcon name="settings" className="size-3.5" />}
    </span>
  )
}

// 左侧栏顶部的范围切换：收起时是白色卡片，显示当前范围；展开后依次列出“全局”、各个项目（带 Agent 数）与“新建项目”。
// 焦点留在过滤框中，上下键在选项间移动、回车进入；“新建项目”用 Tab 到达；Esc 关闭，焦点回到触发按钮。
// 选择后进入该范围的概览，并记住它（不带项目的旧地址按它跳转）。
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
  const optionKey = (option: Option) => (option.kind === 'global' ? 'global' : option.project.id)
  const optionId = (index: number) => `${listId}-${index}`

  const openChange = (next: boolean) => {
    setOpen(next)
    if (next) {
      setFilter('')
      setActive(Math.max(0, options.findIndex((o) => optionKey(o) === currentId)))
    }
  }

  const choose = (option: Option | undefined) => {
    if (!option) return
    lastScope.set(option.kind === 'global' ? PLATFORM : option.project.id)
    setOpen(false)
    const target: ShellScope = option.kind === 'global' ? GLOBAL : { kind: 'project', name: option.project.name }
    void navigate(overviewPath(target))
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
      choose(options[active])
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
        className={rowClass(isCurrent, index === active)}
      >
        <span aria-hidden="true" className="inline-flex w-[18px]">
          {isCurrent && <CheckIcon className="size-4" strokeWidth={3} />}
        </span>
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate">{item.kind === 'global' ? text.global : item.project.name}</span>
          {item.kind === 'project' && item.project.isDefault && (
            <span className="shrink-0 rounded-stamp bg-white px-[7px] text-[12px] leading-5 font-black shadow-[inset_0_0_0_1.5px_var(--ink)]">
              {t.projects.switcher.isDefault}
            </span>
          )}
        </span>
        {item.kind === 'project' ? (
          <span className={cn('text-[12.5px] font-bold whitespace-nowrap', isCurrent ? 'text-ink-700' : 'text-ink-500')}>
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
            'flex h-10 w-full items-center gap-2 rounded-input border-2 border-ink bg-white px-2.5 text-[13.5px] text-ink shadow-print-ink outline-none focus-visible:shadow-focus',
            open && 'shadow-[3px_3px_0_var(--ink),0_0_0_3px_var(--riso-yellow)]',
          )}
        >
          <ScopeMark scope={scope} />
          <span className="min-w-0 truncate font-black">{name}</span>
          <NavIcon name="updown" className="ml-auto size-3.5 [stroke-width:2.4]" />
        </PopoverPrimitive.Trigger>
        <PopoverPrimitive.Portal>
          <PopoverPrimitive.Content
            align="start"
            sideOffset={8}
            collisionPadding={16}
            aria-label={text.scope.list}
            className="z-50 flex w-[min(320px,calc(100vw-32px))] flex-col gap-1.5 rounded-card bg-paper-raised p-2 text-ink shadow-[0_0_0_2px_var(--ink),4px_4px_0_2px_var(--riso-blue)] outline-none"
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
              aria-activedescendant={options[active] ? optionId(active) : undefined}
              value={filter}
              onChange={(event) => {
                setFilter(event.target.value)
                setActive(0)
              }}
              onKeyDown={onKeyDown}
              className="h-[38px] w-full rounded-full border-0 bg-tint-blue px-3.5 text-[13.5px] text-ink shadow-[inset_0_0_0_2px_var(--ink)] outline-none placeholder:text-ink-500"
            />
            <div id={listId} role="listbox" aria-label={text.scope.list} className="flex flex-col gap-0.5">
              <ul role="none" className="flex flex-col">
                {option(options[0], 0)}
              </ul>
              <span aria-hidden="true" className="mx-1.5 my-1 border-t-2 border-dashed border-ink/18" />
              <span id={groupId} className="px-3 pt-0.5 text-[12px] font-black text-ink-500">
                {text.scope.projects(shown.length)}
              </span>
              {/* 最多显示 7 行，超出时在列表内滚动；窄屏时最高半屏。 */}
              <ul role="group" aria-labelledby={groupId} className="flex max-h-[min(280px,50vh)] flex-col gap-0.5 overflow-y-auto">
                {shown.length === 0 && (
                  <li role="presentation" className="px-3 py-2 text-[13.5px] text-ink-500">
                    {text.scope.noMatch}
                  </li>
                )}
                {options.slice(1).map((item, index) => option(item, index + 1))}
              </ul>
            </div>
            <span aria-hidden="true" className="mx-1.5 my-1 border-t-2 border-dashed border-ink/18" />
            <button
              type="button"
              className="flex min-h-[38px] items-center gap-2.5 rounded-ticket px-3 text-[14px] font-black text-ink outline-none hover:bg-tint-blue focus-visible:shadow-focus"
              onClick={() => {
                setOpen(false)
                setCreating(true)
              }}
            >
              <PlusIcon className="size-4" strokeWidth={3} />
              {text.scope.create}
            </button>
          </PopoverPrimitive.Content>
        </PopoverPrimitive.Portal>
      </PopoverPrimitive.Root>
      {creating && <CreateProjectDialog onClose={() => setCreating(false)} />}
    </>
  )
}
