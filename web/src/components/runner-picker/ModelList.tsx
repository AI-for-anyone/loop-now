import { useEffect, useId, useRef, type KeyboardEvent, type RefObject } from 'react'
import { CheckIcon, ChevronRightIcon, SearchIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { ScrollRuler } from '@/components/PrecisionRuler'
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
  listMaxHeightClass?: string
  searchRef: RefObject<HTMLInputElement | null>
}

// Context windows as a short figure: 200K, 1M.
const contextFigure = (tokens: number) => (tokens >= 1_000_000 ? `${+(tokens / 1_000_000).toFixed(1)}M` : tokens >= 1000 ? `${Math.round(tokens / 1000)}K` : tokens > 0 ? String(tokens) : '')

// The searchable model list of the runtime being looked at. The search box drives the list (a combobox whose popup is a tree): up and
// down move through the rows, Enter chooses a model or opens a provider, right opens a provider, left closes it or goes back to the
// runtimes, and right at the end of the text moves on to the thinking level. Under a runtime with several providers each provider is a
// row of its own that opens to its models.
export function ModelList(props: ModelListProps) {
  const {
    rows,
    hidden,
    grouped,
    query,
    onQuery,
    active,
    onActive,
    chosen,
    loading,
    reading,
    failures,
    reasonOf,
    blocked,
    onToggle,
    onChoose,
    onRail,
    onThinking,
    autoFocus,
    listMaxHeightClass = 'max-h-[220px]',
    searchRef,
  } = props
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

  // Open on the chosen model: once it is listed, scroll the list (only the list, not the page) so it sits in view.
  const listRef = useRef<HTMLUListElement>(null)
  const placed = useRef(false)
  useEffect(() => {
    const list = listRef.current
    if (placed.current || !list || !chosen) return
    const row = document.getElementById(rowId(chosen))
    if (!row) return
    placed.current = true
    const top = row.getBoundingClientRect().top - list.getBoundingClientRect().top + list.scrollTop
    if (top + row.offsetHeight > list.clientHeight) list.scrollTop = Math.max(0, top - list.clientHeight / 3)
  })

  // Escape with text in the search box clears the search. Dialogs and popovers close on Escape from a listener on the document in the
  // capture phase, before the search box hears it, so this listens on the window, earlier still, and keeps the key from reaching them.
  const clear = useRef(onQuery)
  useEffect(() => {
    clear.current = onQuery
  })
  useEffect(() => {
    const onEscape = (event: globalThis.KeyboardEvent) => {
      const input = searchRef.current
      if (event.key !== 'Escape' || !input || document.activeElement !== input || input.value === '') return
      event.preventDefault()
      event.stopPropagation()
      clear.current('')
    }
    window.addEventListener('keydown', onEscape, true)
    return () => window.removeEventListener('keydown', onEscape, true)
  }, [searchRef])

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
      // Enter never submits the form around the picker, even with nothing listed.
      event.preventDefault()
      if (!current) return
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
        title={t.config.basic.modelOption(row.model.name, row.model.id)}
        onMouseMove={() => !isActive && onActive(row.key)}
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => !locked && onChoose(row.provider, row.model)}
        className={cn(
          'relative flex min-h-8 cursor-pointer items-center gap-2 rounded-ticket py-1.5 pr-2.5 text-[14px] transition-colors',
          row.grouped ? 'pl-6' : 'pl-2.5',
          isChosen && 'bg-surface-active/80',
          // The keyboard's row highlights while the search box has focus; the mouse gets a clean hover.
          isActive &&
            cn(
              'group-focus-within/list:bg-surface-hover',
              isChosen && 'group-focus-within/list:bg-surface-active',
            ),
          !locked && !isChosen && 'hover:bg-surface-hover',
          locked && 'cursor-not-allowed opacity-45',
        )}
      >
        <span className="flex min-w-0 flex-1 items-baseline gap-2">
          <span className={cn('relative min-w-0 truncate text-ink', isChosen ? 'font-semibold' : 'font-medium')}>
            {row.model.name}
          </span>
          {row.model.name !== row.model.id && (
            <span className="min-w-0 truncate font-mono text-[12px] text-ink-500">{row.model.id}</span>
          )}
        </span>
        <span className="flex shrink-0 items-center gap-2">
          {figure && <span className="font-mono text-[12px] font-normal text-ink-500 tabular-nums">{figure}</span>}
          {isChosen && <CheckIcon aria-hidden="true" className="size-3.5 shrink-0 text-riso-blue" strokeWidth={2.5} />}
        </span>
      </li>
    )
  }

  return (
    <div className="group/list flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex h-10 shrink-0 items-center gap-2 border-b border-hairline px-3">
        <SearchIcon aria-hidden="true" className="size-4 shrink-0 text-ink-500" />
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
          className="min-w-0 flex-1 bg-transparent text-[14px] text-ink outline-none placeholder:text-ink-500"
        />
      </div>
      <div className="relative flex min-h-0 flex-1 flex-col">
        <ul
          ref={listRef}
          id={treeId}
          role="tree"
          aria-label={copy.models}
          className={cn('no-scrollbar min-h-0 flex-1 overflow-y-auto overscroll-contain py-1 pr-6 pl-1.5 sm:min-h-[150px]', listMaxHeightClass)}
        >
          {blocks.map((block, blockIndex) => {
            if (block.section) {
              return (
                <li
                  key={block.section.key}
                  role="none"
                  className={cn(
                    'flex items-center gap-1.5 px-2 pb-1 text-[11.5px] font-semibold text-ink-500',
                    blockIndex > 0 ? 'pt-2.5' : 'pt-0.5',
                  )}
                >
                  {block.section.usable && <span aria-hidden="true" className="size-1.5 shrink-0 rounded-full bg-green" />}
                  <span className="shrink-0">{copy.usable[String(block.section.usable) as 'true' | 'false']}</span>
                  <span aria-hidden="true" className="h-0 flex-1 border-t border-hairline" />
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
                    'sticky top-0 z-[1] flex min-h-8 cursor-pointer items-center gap-1.5 rounded-ticket bg-surface-elevated px-2 py-1 text-[13.5px] transition-colors',
                    active === key ? 'group-focus-within/list:bg-surface-hover' : 'hover:bg-surface-hover',
                    locked && 'opacity-60',
                  )}
                >
                  <ChevronRightIcon aria-hidden="true" className={cn('size-3.5 shrink-0 text-ink-500 transition-transform motion-reduce:transition-none', expanded && 'rotate-90')} />
                  <span className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2">
                    <span className="font-mono text-[13.5px] font-semibold text-ink">{provider.id}</span>
                    {provider.source === 'plugin' && <span className="min-w-0 truncate text-[12px] font-medium text-ink-700">{[provider.name, pluginName(provider)].filter(Boolean).join(' · ')}</span>}
                    {reason && <span className={cn('text-[12px] font-medium', locked ? 'text-red-text' : 'text-ink-500')}>{t.config.basic.pluginUnavailable[reason]}</span>}
                  </span>
                  <span className="shrink-0 rounded-stamp bg-surface-subtle px-1.5 py-0.5 font-mono text-[11px] leading-none font-normal text-ink-500">
                    {matched === null ? copy.modelCount(provider.modelCount) : copy.matchCount(matched, provider.modelCount)}
                  </span>
                </div>
                {expanded && (
                  <ul role="group">
                    {block.models.map(modelRow)}
                    {reading.has(provider.id) && <li role="none" className="py-1.5 pl-6 text-[12.5px] text-ink-500">{copy.loading}</li>}
                    {failures.has(provider.id) && <Failure error={failures.get(provider.id)} />}
                  </ul>
                )}
              </li>
            )
          })}
          {!grouped && [...failures.values()].slice(0, 1).map((error, index) => <Failure key={index} error={error} />)}
          {loading && rows.length === 0 && <li role="none" className="px-2.5 py-2.5 text-[13px] text-ink-500">{copy.loading}</li>}
          {!loading && rows.length === 0 && failures.size === 0 && <li role="none" className="px-2.5 py-2.5 text-[13px] text-ink-500">{copy.empty}</li>}
          {hidden > 0 && <li role="none" className="px-2.5 pt-1.5 pb-1 text-[12px] text-ink-500">{copy.more(hidden)}</li>}
        </ul>
        <ScrollRuler targetRef={listRef} label={copy.models} tickCount={11} className="inset-y-2 right-1" />
      </div>
    </div>
  )
}

// A provider's models could not be read: why, in the list where they would be.
function Failure({ error }: { error: unknown }) {
  return (
    <li role="none" className="grid gap-0.5 py-1.5 pr-2 pl-6 text-[12px] text-red-text">
      <span className="font-semibold">{t.config.basic.catalogUnavailable}</span>
      <span>{t.config.basic.catalogReason(describeError(error))}</span>
    </li>
  )
}
