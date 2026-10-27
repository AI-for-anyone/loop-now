import { useState, type DragEvent, type KeyboardEvent } from 'react'
import { ChevronRightIcon, HardDriveIcon, LoaderCircleIcon } from 'lucide-react'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { ENTRY_FILE, isEntryFile, nameProblem, type FolderEntry, type FolderSource } from './folder-source'
import { useFolderList } from './folder-queries'
import { EntryBadge, EntryIcon, EntryMenu, type EntryActions } from './FolderParts'

// 正在新建或重命名的那一项：在树里就地输入名称。
export type Naming = { mode: 'create'; dir: string; type: 'file' | 'dir' } | { mode: 'rename'; entry: FolderEntry }

interface TreeContext {
  source: FolderSource
  expanded: ReadonlySet<string>
  selected: string
  naming: Naming | null
  dropTarget: string | null
  actions: EntryActions
  onToggle: (path: string) => void
  // 点击树里的一行：选中它，但不改变它自己的展开状态（展开与收起由同一次点击的 onToggle 负责）。
  onSelect: (entry: FolderEntry) => void
  onName: (value: string) => void
  onNameCancel: () => void
  onDragTarget: (path: string | null) => void
  onDropOn: (dir: string, event: DragEvent) => void
}

const INDENT = 14
const pad = (depth: number) => ({ paddingLeft: 8 + depth * INDENT })
// 缩进参考线：每一级一条竖线，深层目录里也能看清层级。
function Guides({ depth }: { depth: number }) {
  return Array.from({ length: depth }, (_, level) => <span key={level} aria-hidden="true" className="pointer-events-none absolute inset-y-0 border-l border-ink/10" style={{ left: 8 + level * INDENT + 7 }} />)
}

const rowClass = 'group relative flex h-7 cursor-pointer items-center gap-1.5 rounded-[6px] pr-1 text-ink outline-none select-none hover:bg-ink/[0.045] focus-visible:shadow-[inset_0_0_0_2px_var(--riso-blue)]'
const selectedClass = 'bg-tint-blue font-bold hover:bg-tint-blue'
const dropClass = 'bg-tint-yellow shadow-[inset_0_0_0_1.5px_var(--ink)] hover:bg-tint-yellow'

// 文件树：根目录一行，其下逐级展开，只读取展开了的文件夹。行按 DOM 顺序平铺，上下键按显示顺序移动。
export function FileTree({ rootLabel, ...ctx }: TreeContext & { rootLabel: string }) {
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const rows = [...event.currentTarget.querySelectorAll<HTMLElement>('[role=treeitem]')]
    const index = rows.indexOf(document.activeElement as HTMLElement)
    const go = (next: HTMLElement | undefined) => { if (next) { event.preventDefault(); next.focus() } }
    if (event.key === 'ArrowDown') go(rows[index + 1])
    else if (event.key === 'ArrowUp') go(rows[index - 1])
    else if (event.key === 'Home') go(rows[0])
    else if (event.key === 'End') go(rows.at(-1))
    else if (event.key === 'ArrowLeft' && index >= 0) {
      // 已收起的项：回到上一级文件夹。
      const level = Number(rows[index].getAttribute('aria-level'))
      if (rows[index].getAttribute('aria-expanded') !== 'true') go(rows.slice(0, index).reverse().find((row) => Number(row.getAttribute('aria-level')) === level - 1))
    }
  }
  const rootSelected = ctx.selected === ''
  return (
    <div role="tree" aria-label={t.folders.tree} onKeyDown={onKeyDown} className="flex min-w-0 flex-col py-1.5 text-[13px]">
      <div
        role="treeitem"
        aria-level={1}
        aria-selected={rootSelected}
        aria-expanded
        tabIndex={rootSelected ? 0 : -1}
        onClick={() => ctx.actions.open(null)}
        onKeyDown={(event) => { if (event.key === 'Enter') ctx.actions.open(null) }}
        onDragOver={(event) => { event.preventDefault(); ctx.onDragTarget('') }}
        onDrop={(event) => ctx.onDropOn('', event)}
        className={cn(rowClass, 'mx-1.5 gap-2 pl-2 font-black', rootSelected && selectedClass, ctx.dropTarget === '' && dropClass)}
      >
        <HardDriveIcon aria-hidden="true" className="size-4 shrink-0 text-ink-700" strokeWidth={2} />
        <span className="min-w-0 flex-1 truncate">{rootLabel}</span>
      </div>
      <TreeLevel ctx={ctx} path="" depth={1} />
    </div>
  )
}

function TreeLevel({ ctx, path, depth }: { ctx: TreeContext; path: string; depth: number }) {
  const list = useFolderList(ctx.source, path)
  const creating = ctx.naming?.mode === 'create' && ctx.naming.dir === path ? ctx.naming : null
  if (list.isPending) return <StatusRow depth={depth} loading>{t.folders.loading}</StatusRow>
  if (list.isError) return <StatusRow depth={depth} error>{list.error instanceof Error ? list.error.message : String(list.error)}</StatusRow>
  const entries = list.data.pages.flatMap((page) => page.entries)
  // 根目录的 AGENTS.md 排在最前：它是 Agent 每次启动时先读的入口。
  const ordered = path === '' ? [...entries].sort((a, b) => Number(b.name === ENTRY_FILE && b.type === 'file') - Number(a.name === ENTRY_FILE && a.type === 'file')) : entries
  return (
    <>
      {creating && <NameRow ctx={ctx} depth={depth} type={creating.type} initial="" />}
      {ordered.map((entry) => <TreeNode key={entry.path} ctx={ctx} entry={entry} depth={depth} />)}
      {list.hasNextPage && (
        <div role="treeitem" aria-level={depth + 1} tabIndex={-1} onClick={() => void list.fetchNextPage()} onKeyDown={(event) => { if (event.key === 'Enter') void list.fetchNextPage() }} className={cn(rowClass, 'mx-1.5 text-[12.5px] font-bold text-riso-blue')} style={pad(depth)}>
          <Guides depth={depth} />
          <span className="w-3.5 shrink-0" />
          {list.isFetchingNextPage ? t.folders.loading : t.folders.showMore}
        </div>
      )}
      {entries.length === 0 && !creating && <StatusRow depth={depth}>{t.folders.empty}</StatusRow>}
    </>
  )
}

function StatusRow({ depth, loading = false, error = false, children }: { depth: number; loading?: boolean; error?: boolean; children: string }) {
  return (
    <div className={cn('relative mx-1.5 flex h-7 items-center gap-1.5 pr-2 text-[12.5px]', error ? 'text-red-text' : 'text-ink-500')} style={pad(depth)}>
      <Guides depth={depth} />
      <span className="flex w-3.5 shrink-0 justify-center">{loading && <LoaderCircleIcon aria-hidden="true" className="riso-spin size-3 motion-reduce:animate-none" />}</span>
      <span className="min-w-0 truncate">{children}</span>
    </div>
  )
}

function TreeNode({ ctx, entry, depth }: { ctx: TreeContext; entry: FolderEntry; depth: number }) {
  const path = entry.path, isDir = entry.type === 'dir', open = isDir && ctx.expanded.has(path)
  const selected = ctx.selected === path
  const [menuOpen, setMenuOpen] = useState(false)
  if (ctx.naming?.mode === 'rename' && ctx.naming.entry.path === path) return <NameRow ctx={ctx} depth={depth} type={entry.type === 'dir' ? 'dir' : 'file'} initial={entry.name} />
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.target !== event.currentTarget) return
    if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); ctx.actions.open(entry) }
    else if (event.key === 'ArrowRight' && isDir && !open) { event.preventDefault(); ctx.onToggle(path) }
    else if (event.key === 'ArrowLeft' && open) { event.preventDefault(); event.stopPropagation(); ctx.onToggle(path) }
    else if (event.key === 'F2') { event.preventDefault(); ctx.actions.rename(entry) }
    else if (event.key === 'Delete' || (event.key === 'Backspace' && event.metaKey)) { event.preventDefault(); ctx.actions.remove(entry) }
  }
  return (
    <>
      <div
        role="treeitem"
        aria-level={depth + 1}
        aria-selected={selected}
        aria-expanded={isDir ? open : undefined}
        tabIndex={selected ? 0 : -1}
        data-path={path}
        draggable
        onDragStart={(event) => { event.dataTransfer.setData('application/x-loop-now-path', path); event.dataTransfer.effectAllowed = 'move' }}
        onDragOver={(event) => { if (!isDir) return; event.preventDefault(); event.stopPropagation(); ctx.onDragTarget(path) }}
        onDrop={(event) => { if (isDir) { event.stopPropagation(); ctx.onDropOn(path, event) } }}
        onClick={() => { if (isDir) ctx.onToggle(path); ctx.onSelect(entry) }}
        onKeyDown={onKeyDown}
        className={cn(rowClass, 'mx-1.5', selected && selectedClass, ctx.dropTarget === path && dropClass, entry.type === 'other' && 'text-ink-500')}
        style={pad(depth)}
      >
        <Guides depth={depth} />
        {isDir ? <ChevronRightIcon aria-hidden="true" className={cn('size-3.5 shrink-0 text-ink-500 transition-transform duration-100 motion-reduce:transition-none', open && 'rotate-90')} strokeWidth={2.4} /> : <span className="w-3.5 shrink-0" />}
        <EntryIcon path={path} type={entry.type} open={open} />
        <span className={cn('min-w-0 flex-1 truncate', isEntryFile(path) && 'font-bold')} title={entry.symlinkTarget ? `${entry.name} → ${entry.symlinkTarget}` : entry.name}>{entry.name}</span>
        {isEntryFile(path) && <EntryBadge />}
        <EntryMenu entry={entry} actions={ctx.actions} onOpenChange={setMenuOpen} className={cn('opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100 focus-visible:opacity-100', menuOpen && 'opacity-100')} />
      </div>
      {open && <TreeLevel ctx={ctx} path={path} depth={depth + 1} />}
    </>
  )
}

// 就地输入名称：Enter 确认，Esc 取消；失去焦点时名称有效就确认，否则取消。
function NameRow({ ctx, depth, type, initial }: { ctx: TreeContext; depth: number; type: 'file' | 'dir'; initial: string }) {
  const [value, setValue] = useState(initial)
  const [touched, setTouched] = useState(false)
  const problem = nameProblem(value)
  const commit = () => { if (problem || value === initial) ctx.onNameCancel(); else ctx.onName(value.trim()) }
  return (
    <div className="relative mx-1.5 flex flex-col py-0.5" style={pad(depth)}>
      <Guides depth={depth} />
      <div className="flex h-6 items-center gap-1.5 pr-1">
        <span className="w-3.5 shrink-0" />
        <EntryIcon path={value} type={type} />
        <input
          autoFocus
          value={value}
          aria-label={type === 'dir' ? t.folders.name.folder : t.folders.name.file}
          aria-invalid={touched && !!problem}
          onFocus={(event) => { const dot = initial.lastIndexOf('.'); event.currentTarget.setSelectionRange(0, type === 'file' && dot > 0 ? dot : initial.length) }}
          onChange={(event) => { setValue(event.target.value); setTouched(true) }}
          onKeyDown={(event) => {
            event.stopPropagation()
            if (event.key === 'Enter') { event.preventDefault(); if (problem) setTouched(true); else commit() }
            if (event.key === 'Escape') { event.preventDefault(); ctx.onNameCancel() }
          }}
          onBlur={commit}
          className="h-6 min-w-0 flex-1 rounded-[5px] border border-riso-blue bg-white px-1.5 text-[13px] text-ink shadow-[0_0_0_3px_rgb(36_67_181/.15)] outline-none aria-invalid:border-red aria-invalid:shadow-[0_0_0_3px_rgb(217_56_30/.15)]"
        />
      </div>
      {touched && problem && <p role="alert" className="py-0.5 pl-9 text-[11.5px] font-bold text-red-text">{t.folders.name.problems[problem]}</p>}
    </div>
  )
}
