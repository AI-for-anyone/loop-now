import { useEffect, useId, useRef, useState, type KeyboardEvent } from 'react'
import { CheckIcon, ChevronDownIcon, SearchIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { Badge } from '@/components/ui/badge'
import { Label } from '@/components/ui/label'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { t } from '@/i18n'
import { fieldClass } from '@/lib/field'
import { cn } from '@/lib/utils'
import { matchesProvider, providerGroups, providerInitial, type ProviderEntry } from './provider-catalog'

const text = t.credentials.providerPicker

// 添加模型账号时选提供方：按钮打开面板，搜索框自动聚焦、输入即筛；顶部一排常用提供方（已有账号的排最前），列表按用途分组。
// 上下键移动、回车选中、Esc 收起。选中后面板收起，换成黄色选中条：地区变体用分段按钮切换，"更换"重开面板。value 是具体的提供方 id。
export function ProviderPicker({ label, entries, accounts, value, open, disabled, onOpenChange, onChange }: {
  label: string
  entries: ProviderEntry[]
  accounts: Schemas['ProjectCredentialAccount'][]
  value: string
  open: boolean
  disabled?: boolean
  onOpenChange: (open: boolean) => void
  onChange: (id: string) => void
}) {
  const id = useId(), listId = `${id}-list`, labelId = `${id}-label`
  const [query, setQuery] = useState(''), [active, setActive] = useState(-1)
  const search = useRef<HTMLInputElement>(null), trigger = useRef<HTMLButtonElement>(null), change = useRef<HTMLButtonElement>(null)
  // 面板收起后焦点回到触发按钮（还没选）或选中条的"更换"（已选）。
  const restoreFocus = useRef(false)
  const selected = entries.find(entry => entry.options.some(option => option.id === value))
  const existing = (entry: ProviderEntry) => accounts.filter(account => entry.options.some(option => option.id === account.provider)).length
  const visible = entries.filter(entry => matchesProvider(entry, query))
  const chips = query.trim() ? [] : entries.filter(entry => entry.recommended || existing(entry) > 0).sort((a, b) => existing(b) - existing(a))
  const optionId = (entry: ProviderEntry) => `${id}-${entry.key}`

  const chosen = !!selected
  useEffect(() => {
    if (open) search.current?.focus()
    else if (restoreFocus.current) (chosen ? change : trigger).current?.focus()
    restoreFocus.current = false
  }, [open, chosen])

  const setOpen = (next: boolean) => {
    setQuery(''); setActive(-1)
    if (!next) restoreFocus.current = true
    onOpenChange(next)
  }
  const pick = (entry: ProviderEntry) => {
    // 同一家换回来时保留原来选的变体，否则取第一个。
    onChange(entry === selected ? value : entry.options[0].id)
    setOpen(false)
  }
  const move = (next: number) => {
    setActive(next)
    if (visible[next]) document.getElementById(optionId(visible[next]))?.scrollIntoView?.({ block: 'nearest' })
  }
  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'ArrowDown') { event.preventDefault(); move(Math.min(active + 1, visible.length - 1)) }
    else if (event.key === 'ArrowUp') { event.preventDefault(); move(Math.max(active - 1, 0)) }
    else if (event.key === 'Enter' && visible[active]) { event.preventDefault(); pick(visible[active]) }
  }

  return <div className="grid min-w-0 gap-2" onKeyDown={event => { if (open && event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); setOpen(false) } }}>
    <Label id={labelId} htmlFor={`${id}-trigger`}>{label}</Label>
    {selected && !open
      ? <div role="group" aria-labelledby={labelId} className="flex min-w-0 flex-wrap items-center gap-x-3.5 gap-y-2.5 rounded-input bg-riso-yellow px-3.5 py-3 text-ink shadow-[inset_0_0_0_2px_var(--ink)]">
        <ProviderAvatar entry={selected} />
        <span className="font-bold [overflow-wrap:anywhere]">{selected.name}</span>
        {selected.name !== value && <span className="font-mono text-[12px] [overflow-wrap:anywhere]">{value}</span>}
        {selected.plugin !== null && <PluginBadge name={selected.plugin} />}
        {selected.options.length > 1 && <ToggleGroup aria-label={text.region} value={value} onValueChange={onChange} className="bg-paper-raised shadow-[inset_0_0_0_1.5px_var(--ink)]">
          {selected.options.map(option => <ToggleGroupItem key={option.id} value={option.id} className="min-h-[30px] px-3 text-[13px] data-[state=on]:bg-ink data-[state=on]:text-white data-[state=on]:shadow-none">{option.label}</ToggleGroupItem>)}
        </ToggleGroup>}
        <button ref={change} type="button" disabled={disabled} onClick={() => setOpen(true)} className="ml-auto rounded-ticket px-1 font-bold underline outline-none focus-visible:shadow-focus">{text.change}</button>
      </div>
      : <button ref={trigger} id={`${id}-trigger`} type="button" aria-haspopup="listbox" aria-expanded={open} aria-controls={open ? `${id}-panel` : undefined} disabled={disabled} onClick={() => setOpen(!open)} className={cn(fieldClass, 'flex h-12 items-center gap-2.5 text-left aria-expanded:shadow-[inset_0_0_0_2px_var(--ink),0_0_0_4px_var(--riso-yellow)]')}>
        <span className="min-w-0 flex-1 truncate text-ink-500">{text.trigger}</span>
        <ChevronDownIcon aria-hidden="true" className="size-4 shrink-0" />
      </button>}
    {open && <div id={`${id}-panel`} className="grid min-w-0 overflow-hidden rounded-card bg-paper-raised text-ink shadow-[0_0_0_2px_var(--ink),6px_6px_0_2px_var(--riso-pink)]">
      <div className="flex items-center gap-2 border-b border-ink/10 px-3 py-2.5">
        <SearchIcon aria-hidden="true" className="size-4 shrink-0 text-ink-500" />
        <input ref={search} type="search" autoComplete="off" aria-label={text.search} placeholder={text.searchPlaceholder} aria-controls={listId} aria-activedescendant={visible[active] ? optionId(visible[active]) : undefined} value={query} onChange={event => { setQuery(event.target.value); setActive(-1) }} onKeyDown={onKeyDown} className="h-[38px] min-w-0 flex-1 rounded-full border-0 bg-tint-blue px-3.5 text-[14px] text-ink outline-none placeholder:text-ink-500 focus-visible:shadow-[inset_0_0_0_2px_var(--ink)]" />
      </div>
      {chips.length > 0 && <div role="group" aria-label={text.recommended} className="flex flex-wrap gap-1.5 px-3 pt-2.5 pb-1">
        {chips.map(entry => <button key={entry.key} type="button" onClick={() => pick(entry)} className="inline-flex min-h-[28px] items-center gap-1 rounded-full bg-paper px-2.5 text-[13px] font-bold text-ink shadow-[inset_0_0_0_1px_rgb(24_38_94/.18)] outline-none hover:shadow-[inset_0_0_0_1px_var(--riso-blue)] focus-visible:shadow-focus">
          {existing(entry) > 0 && <CheckIcon aria-hidden="true" className="size-3.5" />}{entry.name}
        </button>)}
      </div>}
      <div id={listId} role="listbox" aria-label={text.list} className="max-h-[420px] overflow-y-auto px-1.5 pt-1.5 pb-2.5">
        {providerGroups.map(group => {
          const items = visible.filter(entry => entry.group === group)
          if (!items.length) return null
          return <div key={group} role="group" aria-labelledby={`${id}-group-${group}`}>
            <div id={`${id}-group-${group}`} className="px-2 pt-2.5 pb-1 text-[12px] font-black text-ink-500">{text.groups[group]}</div>
            {items.map(entry => {
              const index = visible.indexOf(entry), count = existing(entry), first = entry.options[0].id
              return <div key={entry.key} id={optionId(entry)} role="option" aria-selected={index === active} onMouseDown={event => event.preventDefault()} onMouseEnter={() => setActive(index)} onClick={() => pick(entry)} className="grid cursor-pointer grid-cols-[30px_minmax(0,1fr)] items-center gap-x-2.5 gap-y-1 rounded-ticket px-2 py-2 aria-selected:bg-tint-blue sm:grid-cols-[30px_minmax(0,1fr)_auto]">
                <ProviderAvatar entry={entry} />
                <span className="flex min-w-0 flex-wrap items-baseline gap-x-2"><span className="font-bold [overflow-wrap:anywhere]">{entry.name}</span>{entry.name !== first && <span className="font-mono text-[12px] text-ink-500 [overflow-wrap:anywhere]">{entry.options.length > 1 ? `${first} +${entry.options.length - 1}` : first}</span>}{entry.plugin !== null && <PluginBadge name={entry.plugin} />}</span>
                <span className="col-start-2 flex flex-wrap items-center gap-2 sm:col-start-3 sm:justify-end">
                  {count > 0 && <Badge className="bg-tint-pink text-pink-text">{text.existing(count)}</Badge>}
                  <AuthBadges methods={entry.authMethods} />
                  <span className="font-mono text-[12px] whitespace-nowrap text-ink-500 tabular-nums">{text.models(entry.modelCount)}</span>
                </span>
              </div>
            })}
          </div>
        })}
      </div>
      {visible.length === 0 && <p role="status" className="px-5 pb-5 text-center text-[13.5px] text-ink-500">{text.empty}</p>}
    </div>}
  </div>
}

// 认证方式徽章：登录（浏览器授权）与 API 密钥。
export function AuthBadges({ methods }: { methods: readonly (string | null)[] }) {
  return <>
    {methods.includes('oauth') && <Badge tone="success">{text.oauth}</Badge>}
    {methods.includes('api_key') && <Badge tone="info">{text.apiKey}</Badge>}
  </>
}

// 插件提供方：注册它的插件。
function PluginBadge({ name }: { name: string }) {
  return <Badge tone="neutral" title={text.plugin(name)} className="max-w-full self-center"><span className="truncate">{text.plugin(name)}</span></Badge>
}

function ProviderAvatar({ entry }: { entry: ProviderEntry }) {
  return <span aria-hidden="true" style={{ background: entry.color }} className="grid size-[30px] shrink-0 place-items-center rounded-ticket font-display-latin text-[13px] font-extrabold text-white">{providerInitial(entry.name)}</span>
}
