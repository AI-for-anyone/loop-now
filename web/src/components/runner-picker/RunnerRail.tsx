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
  // Down arrow leaves the horizontal tab bar for the model search or lead panel.
  onLeave: () => void
}

// Inline status cue inside a horizontal runtime tab pill: a green dot + muted version for a ready CLI, a subtle "Built in" pill for the
// bundled runtime (full explanation stays in aria-label), or a compact status pill for a CLI with a setup problem.
export function RunnerState({ entry }: { entry: RailEntry }) {
  const copy = t.runtime.picker
  const { readiness, definition } = entry
  if (readiness.state === 'problem') {
    const tone =
      readiness.problem === 'logged_out'
        ? 'bg-tint-yellow text-pink-text [[data-skin=plate]_&]:border [[data-skin=plate]_&]:border-pink-text/50'
        : 'bg-tint-red text-red-text [[data-skin=plate]_&]:border [[data-skin=plate]_&]:border-red-text/50'
    return (
      <span
        className={cn(
          'inline-flex items-center rounded-stamp px-1.5 py-0.5 text-[10.5px] leading-none font-medium whitespace-nowrap [[data-skin=plate]_&]:font-black',
          tone,
        )}
      >
        {problemStamp(definition, readiness.problem)}
      </span>
    )
  }
  if (readiness.state !== 'ready') {
    return <span className="text-[11px] font-normal text-ink-500 [[data-skin=plate]_&]:font-bold">{readiness.state === 'checking' ? copy.checking : copy.unknown}</span>
  }
  if (definition.capabilities.bundled) {
    return (
      <span className="rounded-stamp bg-surface-hover px-1.5 py-0.5 text-[10.5px] leading-none font-medium text-ink-500 [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:bg-ink [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-white">
        {copy.bundled}
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1">
      <span aria-hidden="true" className="size-1.5 shrink-0 rounded-full bg-green [[data-skin=plate]_&]:ring-1 [[data-skin=plate]_&]:ring-ink" />
      {entry.version && <span className="font-mono text-[10.5px] font-normal text-ink-500">{entry.version}</span>}
    </span>
  )
}

// Single-row horizontal runtime pill tab bar at the top of the picker. Choosing a tab shows its models (or setup guidance) below; the
// model, not the tab, is what gets chosen.
export function RunnerRail({ entries, lead, browsed, current, tabId, panelId, onBrowse, onLeave }: RunnerRailProps) {
  const refs = useRef(new Map<string, HTMLButtonElement>())
  // Every tab in order, keyed by its element id: the first entry, then the runtimes.
  const tabs = [...(lead ? [{ id: lead.tabId, browse: lead.onBrowse }] : []), ...entries.map((entry) => ({ id: tabId(entry.definition.id), browse: () => onBrowse(entry.definition.id) }))]
  const move = (event: KeyboardEvent, index: number) => {
    const target =
      event.key === 'ArrowLeft'
        ? index - 1
        : event.key === 'ArrowRight'
          ? index + 1
          : event.key === 'Home'
            ? 0
            : event.key === 'End'
              ? tabs.length - 1
              : null
    if (target === null) {
      if (event.key === 'ArrowDown') {
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
    <div className="border-b border-hairline bg-surface-subtle/60 px-2.5 py-2 [[data-skin=plate]_&]:border-b-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/35 [[data-skin=plate]_&]:bg-paper-tab">
      <div
        role="tablist"
        aria-label={t.runtime.picker.rail}
        aria-orientation="horizontal"
        className="no-scrollbar flex items-center gap-1.5 overflow-x-auto"
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
              mark={<RuntimeMark runtime={definition} className="size-4 shrink-0" />}
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

// One horizontal pill tab in the runtime bar: mark, name, inline status cue, and an in-use dot cleanly inside the pill.
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
        'inline-flex h-8 shrink-0 cursor-pointer items-center gap-1.5 rounded-input border px-2.5 text-[12.5px] whitespace-nowrap transition-colors outline-none [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:focus-visible:shadow-focus',
        selected
          ? 'border-hairline-strong bg-surface-elevated font-semibold text-ink shadow-xs [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:shadow-[2px_2px_0_var(--ink)]'
          : 'border-transparent font-medium text-ink-500 hover:bg-surface-hover hover:text-ink [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-ink-700 [[data-skin=plate]_&]:hover:border-ink/25 [[data-skin=plate]_&]:hover:bg-paper-raised',
      )}
    >
      <span className="inline-flex size-4 shrink-0 items-center justify-center [&>svg]:size-4">{mark}</span>
      <span>{name}</span>
      {state}
      {inUse && (
        <span
          aria-hidden="true"
          className="ml-0.5 size-1.5 shrink-0 rounded-full bg-ink [[data-skin=plate]_&]:size-2 [[data-skin=plate]_&]:bg-riso-pink [[data-skin=plate]_&]:ring-1 [[data-skin=plate]_&]:ring-ink"
        />
      )}
    </button>
  )
}
