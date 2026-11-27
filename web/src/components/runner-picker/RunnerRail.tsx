import { useRef, type KeyboardEvent, type ReactNode } from 'react'
import { t } from '@/i18n'
import type { AgentRuntime } from '@/lib/runtime'
import { cn } from '@/lib/utils'
import { RuntimeMark } from './RuntimeMark'
import { problemStamp, stateText, type RailEntry } from './rail-entry'

// The rail's first entry when it has one that is not a runtime (RunnerPicker's lead).
export interface RailLead {
  name: string
  mark: ReactNode
  state: ReactNode
  label: string
  tabId: string
  selected: boolean
  inUse: boolean
  onBrowse: () => void
}

interface RunnerRailProps {
  entries: RailEntry[]
  lead?: RailLead
  browsed: AgentRuntime | undefined
  current: AgentRuntime | undefined
  tabId: (id: AgentRuntime) => string
  panelId: string
  onBrowse: (id: AgentRuntime) => void
  // Right arrow leaves the vertical rail for the model search or lead panel.
  onLeave: () => void
}

// Inline status cue under a runtime's name: a green dot + muted version (or "Ready") for a ready CLI, a subtle "Built in" pill for the
// bundled runtime (full explanation stays in aria-label), or a compact status pill for a CLI with a setup problem.
export function RunnerState({ entry }: { entry: RailEntry }) {
  const copy = t.runtime.picker
  const { readiness, definition } = entry
  if (readiness.state === 'problem') {
    const tone =
      readiness.problem === 'logged_out'
        ? 'bg-tint-yellow text-pink-text'
        : 'bg-tint-red text-red-text'
    return (
      <span
        className={cn(
          'inline-flex items-center rounded-stamp px-1.5 py-0.5 text-[11px] leading-none font-medium whitespace-nowrap',
          tone,
        )}
      >
        {problemStamp(definition, readiness.problem)}
      </span>
    )
  }
  if (readiness.state !== 'ready') {
    return <span className="text-[11.5px] leading-tight font-normal text-ink-500">{readiness.state === 'checking' ? copy.checking : copy.unknown}</span>
  }
  if (definition.capabilities.bundled) {
    return (
      <span className="rounded-stamp bg-surface-hover px-1.5 py-0.5 text-[11px] leading-none font-medium text-ink-500">
        {copy.bundled}
      </span>
    )
  }
  return (
    <span className="inline-flex min-w-0 items-center gap-1 text-[11.5px] leading-tight font-normal text-ink-500">
      <span aria-hidden="true" className="size-1.5 shrink-0 rounded-full bg-green" />
      <span className={cn('truncate', entry.version && 'font-mono')}>{entry.version ?? copy.ready}</span>
    </span>
  )
}

// Left vertical runtime list on desktop, wrapping into rows on narrow screens (never horizontally scrolling). Choosing a tab shows its
// models (or setup guidance) on the right; the model, not the tab, is what gets chosen.
export function RunnerRail({ entries, lead, browsed, current, tabId, panelId, onBrowse, onLeave }: RunnerRailProps) {
  const refs = useRef(new Map<string, HTMLButtonElement>())
  // Every tab in order, keyed by its element id: the first entry, then the runtimes.
  const tabs = [...(lead ? [{ id: lead.tabId, browse: lead.onBrowse }] : []), ...entries.map((entry) => ({ id: tabId(entry.definition.id), browse: () => onBrowse(entry.definition.id) }))]
  const move = (event: KeyboardEvent, index: number) => {
    const target =
      event.key === 'ArrowUp'
        ? index - 1
        : event.key === 'ArrowDown'
          ? index + 1
          : event.key === 'Home'
            ? 0
            : event.key === 'End'
              ? tabs.length - 1
              : null
    if (target === null) {
      if (event.key === 'ArrowRight') {
        event.preventDefault()
        onLeave()
      }
      return
    }
    event.preventDefault()
    const tab = tabs[(target + tabs.length) % tabs.length]
    tab.browse()
    refs.current.get(tab.id)?.focus()
  }
  const register = (id: string, node: HTMLButtonElement | null) => {
    if (node) refs.current.set(id, node)
    else refs.current.delete(id)
  }
  const offset = lead ? 1 : 0
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-1.5 px-2 py-2 sm:py-2.5">
      <p aria-hidden="true" className="hidden px-2 text-[11.5px] font-semibold text-ink-500 sm:block">
        {t.runtime.picker.rail}
      </p>
      <div
        role="tablist"
        aria-label={t.runtime.picker.rail}
        aria-orientation="vertical"
        className="no-scrollbar flex min-h-0 flex-1 flex-wrap gap-1 overflow-y-auto overscroll-contain sm:flex-col sm:flex-nowrap"
      >
        {lead && (
          <RailTab
            id={lead.tabId}
            register={register}
            label={lead.label}
            selected={lead.selected}
            inUse={lead.inUse}
            panelId={panelId}
            onClick={lead.onBrowse}
            onKeyDown={(event) => move(event, 0)}
            mark={lead.mark}
            name={lead.name}
            state={lead.state}
          />
        )}
        {entries.map((entry, index) => {
          const { definition } = entry
          const id = tabId(definition.id)
          return (
            <RailTab
              key={definition.id}
              id={id}
              register={register}
              label={[definition.name, stateText(entry), definition.id === current ? t.runtime.picker.current : null].filter(Boolean).join(', ')}
              selected={definition.id === browsed}
              inUse={definition.id === current}
              panelId={panelId}
              onClick={() => onBrowse(definition.id)}
              onKeyDown={(event) => move(event, index + offset)}
              mark={<RuntimeMark runtime={definition} className="size-6 shrink-0" />}
              name={definition.name}
              state={<RunnerState entry={entry} />}
            />
          )
        })}
      </div>
    </div>
  )
}

interface RailTabProps {
  id: string
  // Keeps the tab's element by its id, for moving focus with arrow keys.
  register: (id: string, node: HTMLButtonElement | null) => void
  label: string
  selected: boolean
  inUse: boolean
  panelId: string
  onClick: () => void
  onKeyDown: (event: KeyboardEvent) => void
  mark: ReactNode
  name: string
  state: ReactNode
}

// One runtime entry in the left rail: mark, name, inline in-use dot, and status cue cleanly stacked underneath.
function RailTab({ id, register, label, selected, inUse, panelId, onClick, onKeyDown, mark, name, state }: RailTabProps) {
  return (
    <button
      ref={(node) => register(id, node)}
      type="button"
      role="tab"
      id={id}
      aria-selected={selected}
      aria-controls={panelId}
      aria-label={label}
      tabIndex={selected ? 0 : -1}
      onClick={onClick}
      onKeyDown={onKeyDown}
      className={cn(
        'flex shrink-0 cursor-pointer items-center gap-2 rounded-input border px-2 py-1.5 text-left transition-colors outline-none focus-visible:shadow-focus sm:w-full',
        selected
          ? 'border-hairline-strong bg-surface-elevated font-semibold text-ink shadow-xs'
          : 'border-transparent font-medium text-ink-700 hover:bg-surface-hover hover:text-ink',
      )}
    >
      <span className="inline-flex size-6 shrink-0 items-center justify-center [&>svg]:size-6">{mark}</span>
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="flex min-w-0 items-center gap-1.5">
          <span className="truncate text-[13.5px] leading-tight text-ink">{name}</span>
          {inUse && (
            <span
              aria-hidden="true"
              className="ml-auto size-1.5 shrink-0 rounded-full bg-riso-blue"
            />
          )}
        </span>
        <span className="flex min-w-0 items-center">{state}</span>
      </span>
    </button>
  )
}
