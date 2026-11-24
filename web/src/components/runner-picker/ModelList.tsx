import { useEffect, useId, useRef, type KeyboardEvent } from 'react'
import { ChevronRightIcon, SearchIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { describeError } from '@/lib/errors'
import { cn } from '@/lib/utils'
import { pluginName, type Unavailable } from './provider-access'
import type { Row } from './runner-rows'

type Provider = Schemas['ModelProvider']
type Model = Schemas['Model']

interface ModelListProps {
  rows: Row[]
  hidden: number
  grouped: boolean
  query: string
  onQuery: (query: string) => void
  // The keyboard's place in the list (rows[].key).
  active: string | null
  onActive: (key: string | null) => void
  chosen: string | null
  loading: boolean
  // Providers whose models are being read, and those whose models could not be read (with why).
  reading: ReadonlySet<string>
  failures: ReadonlyMap<string, unknown>
  reasonOf: (provider: Provider) => Unavailable | null
  blocked: (provider: Provider) => boolean
  onToggle: (providerId: string) => void
  onChoose: (provider: Provider, model: Model) => void
  onRail: () => void
  onThinking: () => void
  autoFocus?: boolean
  searchRef?: React.Ref<HTMLInputElement>
}

// Context windows as a short figure: 200K, 1M.
const contextFigure = (tokens: number) => (tokens >= 1_000_000 ? `${+(tokens / 1_000_000).toFixed(1)}M` : tokens >= 1000 ? `${Math.round(tokens / 1000)}K` : tokens > 0 ? String(tokens) : '')

// The searchable model list of the runtime being looked at. The search box drives the list (a combobox whose popup is a tree): up and
// down move through the rows, Enter chooses a model or opens a provider, right opens a provider, left closes it or goes back to the
// runtimes, and right at the end of the text moves on to the thinking level. Under a runtime with several providers each provider is a
// row of its own that opens to its models.
export function ModelList(props: ModelListProps) {
  const { rows, hidden, grouped, query, onQuery, active, onActive, chosen, loading, reading, failures, reasonOf, blocked, onToggle, onChoose, onRail, onThinking, autoFocus, searchRef } = props
  const treeId = useId()
  const rowId = (key: string) => `${treeId}-${key.replace(/[^a-zA-Z0-9_-]/g, '_')}`
  const scrolled = useRef<string | null>(null)
  // Headings are not stops for the keyboard.
  const stops = rows.filter((row): row is Exclude<Row, { kind: 'section' }> => row.kind !== 'section')
  const index = stops.findIndex((row) => row.key === active)
  const current = index >= 0 ? stops[index] : undefined

  // Keep the keyboard's row in view when it moves (not when the mouse moves it).
  const keyboardMove = useRef(false)
  useEffect(() => {
    if (!active || !keyboardMove.current || scrolled.current === active) return
    scrolled.current = active
    keyboardMove.current = false
    const row = document.getElementById(rowId(active))
    // A provider's row holds its models too: bring its header into view, not the whole group.
    const target = row?.dataset.row === 'provider' ? row.firstElementChild : row
    target?.scrollIntoView?.({ block: 'nearest' })
  })

  const go = (target: number) => {
    const row = stops[Math.max(0, Math.min(stops.length - 1, target))]
    if (!row) return
    keyboardMove.current = true
    onActive(row.key)
  }
  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    const input = event.currentTarget
    const atStart = input.selectionStart === 0 && input.selectionEnd === 0
    const atEnd = input.selectionStart === input.value.length
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      go(index + 1)
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      go(index - 1)
    } else if (event.key === 'Enter') {
      if (!current) return
      event.preventDefault()
      if (current.kind === 'provider') onToggle(current.provider.id)
      else if (!blocked(current.provider)) onChoose(current.provider, current.model)
    } else if (event.key === 'ArrowRight' && atEnd) {
      event.preventDefault()
      if (current?.kind === 'provider' && !current.expanded && !query) onToggle(current.provider.id)
      else onThinking()
    } else if (event.key === 'ArrowLeft' && atStart) {
      event.preventDefault()
      if (current?.kind === 'provider' && current.expanded && !query) onToggle(current.provider.id)
      else if (current?.kind === 'model' && current.grouped && !query) {
        keyboardMove.current = true
        onActive(`p:${current.provider.id}`)
      } else onRail()
    } else if (event.key === 'Escape' && query) {
      event.preventDefault()
      event.stopPropagation()
      onQuery('')
    }
  }

  const copy = t.runtime.picker
  // Rows nest the way the tree does: each provider's models sit in a group under its row.
  const blocks: { section?: Extract<Row, { kind: 'section' }>; header?: Extract<Row, { kind: 'provider' }>; models: Extract<Row, { kind: 'model' }>[] }[] = []
  for (const row of rows) {
    if (row.kind === 'section') blocks.push({ section: row, models: [] })
    else if (row.kind === 'provider') blocks.push({ header: row, models: [] })
    else if (blocks.length === 0 || !row.grouped) {
      if (blocks.length === 0 || blocks[blocks.length - 1].header) blocks.push({ models: [] })
      blocks[blocks.length - 1].models.push(row)
    } else blocks[blocks.length - 1].models.push(row)
  }

  const modelRow = (row: Extract<Row, { kind: 'model' }>) => {
    const isChosen = row.key === chosen, isActive = row.key === active, locked = blocked(row.provider)
    const figure = contextFigure(row.model.contextWindow)
    return (
      <li
        key={row.key}
        id={rowId(row.key)}
        role="treeitem"
        aria-level={row.grouped ? 2 : 1}
        aria-selected={isChosen}
        aria-disabled={locked || undefined}
        aria-label={t.config.basic.modelOption(row.model.name, row.model.id)}
        onMouseMove={() => !isActive && onActive(row.key)}
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => !locked && onChoose(row.provider, row.model)}
        className={cn(
          'relative grid cursor-pointer grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3 rounded-[10px] py-[7px] pr-2.5 text-[14px]',
          row.grouped ? 'pl-[30px]' : 'pl-[22px]',
          // The keyboard's row is outlined only while the search box has focus; the mouse gets a plain hover.
          isActive ? 'group-focus-within/list:bg-tint-blue group-focus-within/list:shadow-[inset_0_0_0_2px_var(--ink)]' : '',
          !locked && 'hover:bg-tint-blue/70',
          locked && 'cursor-not-allowed opacity-45',
        )}
      >
        {isChosen && <span aria-hidden="true" className={cn('absolute top-1/2 size-0 -translate-y-1/2 border-y-[5px] border-l-[7px] border-y-transparent border-l-riso-pink', row.grouped ? 'left-[14px]' : 'left-[7px]')} />}
        <span className="flex min-w-0 flex-wrap items-baseline gap-x-2">
          <span className="relative min-w-0 font-black text-ink">
            {isChosen && <span aria-hidden="true" className="absolute inset-x-[-4px] top-[45%] bottom-[-1px] -rotate-[1deg] bg-riso-yellow mix-blend-multiply" />}
            <span className="relative">{row.model.name}</span>
          </span>
          {row.model.name !== row.model.id && <span className="min-w-0 truncate font-mono text-[11.5px] text-ink-500">{row.model.id}</span>}
        </span>
        {figure && <span className="font-mono text-[11px] font-medium text-ink-500">{figure}</span>}
      </li>
    )
  }

  return (
    <div className="group/list flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="relative shrink-0 px-3 pt-3 pb-2">
        <SearchIcon aria-hidden="true" className="pointer-events-none absolute top-1/2 left-[25px] size-4 -translate-y-[calc(50%-2px)] text-ink-500" />
        <input
          ref={searchRef}
          type="text"
          role="combobox"
          aria-expanded="true"
          aria-autocomplete="list"
          aria-controls={treeId}
          aria-activedescendant={current ? rowId(current.key) : undefined}
          aria-label={grouped ? copy.searchGrouped : copy.search}
          placeholder={grouped ? copy.searchGrouped : copy.search}
          autoFocus={autoFocus}
          autoComplete="off"
          spellCheck={false}
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          onKeyDown={onKeyDown}
          className="h-10 w-full min-w-0 rounded-full border-0 bg-white pr-4 pl-10 text-[14px] text-ink shadow-[inset_0_0_0_2px_var(--ink)] outline-none placeholder:text-ink-500 focus-visible:shadow-[inset_0_0_0_2px_var(--ink),0_0_0_4px_var(--riso-yellow)]"
        />
      </div>
      <ul id={treeId} role="tree" aria-label={copy.models} className="riso-scrollbar min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 pb-2">
        {blocks.map((block, blockIndex) => {
          if (block.section) {
            // A group heading, set like a margin note: small caps on a rule.
            return (
              <li key={block.section.key} role="none" className={cn('flex items-center gap-2 px-1.5 pb-1 text-[11.5px] font-black tracking-[0.06em] text-ink-500', blockIndex > 0 ? 'pt-3' : 'pt-0.5')}>
                {block.section.usable && <span aria-hidden="true" className="size-[7px] shrink-0 rounded-full bg-green shadow-[0_0_0_1.5px_var(--ink)]" />}
                <span className="shrink-0">{copy.usable[String(block.section.usable) as 'true' | 'false']}</span>
                <span aria-hidden="true" className="h-0 flex-1 border-t-2 border-dashed border-ink/20" />
              </li>
            )
          }
          if (!block.header) return block.models.map(modelRow)
          const { provider, expanded, key, matched } = block.header
          const reason = reasonOf(provider), locked = blocked(provider)
          return (
            <li
              key={key}
              id={rowId(key)}
              data-row="provider"
              role="treeitem"
              aria-level={1}
              aria-expanded={expanded}
              aria-label={[provider.id, provider.source === 'plugin' ? [provider.name, pluginName(provider)].filter(Boolean).join(' · ') : null, reason ? t.config.basic.pluginUnavailable[reason] : null].filter(Boolean).join(', ')}
              aria-selected={false}
              className={cn(blockIndex > 0 && 'mt-0.5')}
            >
              <div
                onMouseMove={() => active !== key && onActive(key)}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => onToggle(provider.id)}
                className={cn(
                  'sticky top-0 z-[1] flex cursor-pointer items-center gap-2 rounded-[10px] bg-paper-raised py-[7px] pr-2.5 pl-1.5',
                  active === key ? 'group-focus-within/list:bg-tint-blue group-focus-within/list:shadow-[inset_0_0_0_2px_var(--ink)]' : 'hover:bg-tint-blue/70',
                  locked && 'opacity-60',
                )}
              >
                <ChevronRightIcon aria-hidden="true" className={cn('size-4 shrink-0 text-ink transition-transform motion-reduce:transition-none', expanded && 'rotate-90')} />
                <span className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2">
                  <span className="font-mono text-[13px] font-bold text-ink">{provider.id}</span>
                  {provider.source === 'plugin' && <span className="min-w-0 truncate text-[12px] font-bold text-ink-700">{[provider.name, pluginName(provider)].filter(Boolean).join(' · ')}</span>}
                  {reason && <span className={cn('text-[12px] font-bold', locked ? 'text-red-text' : 'text-ink-500')}>{t.config.basic.pluginUnavailable[reason]}</span>}
                </span>
                <span className="shrink-0 rounded-full bg-tint-blue px-2 font-mono text-[11px] leading-[18px] font-bold text-ink-700">{matched === null ? copy.modelCount(provider.modelCount) : copy.matchCount(matched, provider.modelCount)}</span>
              </div>
              {expanded && (
                <ul role="group">
                  {block.models.map(modelRow)}
                  {reading.has(provider.id) && <li role="none" className="py-1.5 pl-[30px] text-[12.5px] font-bold text-ink-500">{copy.loading}</li>}
                  {failures.has(provider.id) && <Failure error={failures.get(provider.id)} />}
                </ul>
              )}
            </li>
          )
        })}
        {!grouped && [...failures.values()].slice(0, 1).map((error, index) => <Failure key={index} error={error} />)}
        {loading && rows.length === 0 && <li role="none" className="px-3 py-3 text-[13px] font-bold text-ink-500">{copy.loading}</li>}
        {!loading && rows.length === 0 && failures.size === 0 && <li role="none" className="px-3 py-3 text-[13px] font-bold text-ink-500">{copy.empty}</li>}
        {hidden > 0 && <li role="none" className="px-3 pt-2 pb-1 text-[12.5px] font-bold text-ink-500">{copy.more(hidden)}</li>}
      </ul>
    </div>
  )
}

// A provider's models could not be read: why, in the list where they would be.
function Failure({ error }: { error: unknown }) {
  return (
    <li role="none" className="grid gap-0.5 py-1.5 pr-2 pl-[30px] text-[12.5px] text-red-text">
      <span className="font-bold">{t.config.basic.catalogUnavailable}</span>
      <span>{t.config.basic.catalogReason(describeError(error))}</span>
    </li>
  )
}
