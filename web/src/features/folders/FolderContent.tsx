import { useMemo, useRef, type KeyboardEvent, type ReactNode } from 'react'
import { BookOpenTextIcon, DownloadIcon, LoaderCircleIcon, PencilIcon } from 'lucide-react'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import { ScrollRuler } from '@/components/PrecisionRuler'
import { Markdown } from '@/features/chat/Markdown'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { ENTRY_FILE, isEntryFile, isImage, isMarkdown, languageOf, linksOutside, nameOf, type FolderEntry, type FolderSource, type TextEditLimits, type TextFormat } from './folder-source'
import { useFolderFile, useFolderList } from './folder-queries'
import { EntryBadge, EntryIcon, EntryMenu, ToolButton, type EntryActions } from './FolderParts'
import { highlightCode } from './highlight'

// ---------- Folder ----------

interface DirViewProps {
  source: FolderSource
  path: string
  actions: EntryActions
  onCreateEntry: () => void
  onEditEntry: (entry: FolderEntry) => void
}

// A folder's content list (name, size, modified time); at the root, AGENTS.md is shown below it, or a prompt to create it if missing.
export function DirView({ source, path, actions, onCreateEntry, onEditEntry }: DirViewProps) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const list = useFolderList(source, path)
  const entries = list.data?.pages.flatMap((page) => page.entries) ?? []
  const entry = path === '' ? entries.find((item) => item.name === ENTRY_FILE && item.type === 'file') : undefined
  return (
    <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden">
      <div ref={scrollRef} className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        {path === '' && list.isSuccess && !entry && <EntryMissing onCreate={onCreateEntry} />}
        <ErrorAlert error={list.error} className="mx-3 mt-3 w-auto" />
        {list.isPending && <Pending />}
        {list.isSuccess && entries.length === 0 && <p className="px-4 py-8 text-center text-[13px] text-ink-500">{t.folders.empty}<br />{t.folders.emptyHint}</p>}
        {entries.length > 0 && (
          <table className="w-full table-fixed border-collapse text-[13px]">
            {/* Crisp 1px hairlines in mono; 2px solid header line and 1.5px dashed row dividers in Plate. */}
            <thead className="sticky top-0 z-10 bg-surface-canvas/95 text-[11.5px] font-medium text-ink-500 backdrop-blur-sm [[data-skin=plate]_&]:bg-paper-raised/95 [[data-skin=plate]_&]:font-black">
              <tr className="border-b border-hairline-strong [[data-skin=plate]_&]:border-b-2 [[data-skin=plate]_&]:border-ink">
                <th className="py-1.5 pl-3 text-left font-medium [[data-skin=plate]_&]:font-black">{t.folders.columns.name}</th>
                <th className="w-24 pr-3 text-right font-medium @max-[34rem]:hidden [[data-skin=plate]_&]:font-black">{t.folders.columns.size}</th>
                <th className="w-28 pr-2 text-right font-medium @max-[34rem]:hidden [[data-skin=plate]_&]:font-black">{t.folders.columns.modified}</th>
                <th className="w-9"><span className="sr-only">{t.folders.actions('')}</span></th>
              </tr>
            </thead>
            <tbody>
              {[...entries].sort((a, b) => Number(isEntryFile(b.path)) - Number(isEntryFile(a.path))).map((item) => (
                <tr key={item.path} className="group h-9 border-b border-hairline hover:bg-surface-hover [[data-skin=plate]_&]:border-b-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/25 [[data-skin=plate]_&]:hover:bg-tint-yellow/60">
                  <td className="py-0 pl-3">
                    <button type="button" onClick={() => actions.open(item)} className={cn('flex h-9 w-full min-w-0 cursor-pointer items-center gap-2.5 text-left text-[13px] font-medium outline-none focus-visible:underline [[data-skin=plate]_&]:font-bold', item.type === 'other' && 'text-ink-500')}>
                      <EntryIcon path={item.path} type={item.type} />
                      <span className={cn('min-w-0 truncate group-hover:text-riso-blue group-hover:underline', item.symlinkTarget && 'max-w-[60%] shrink-0', isEntryFile(item.path) && 'font-semibold [[data-skin=plate]_&]:font-black')}>{item.name}</span>
                      {item.symlinkTarget && <span className="min-w-0 truncate text-[11.5px] text-ink-500">→ {item.symlinkTarget}</span>}
                      {isEntryFile(item.path) && <EntryBadge />}
                    </button>
                  </td>
                  <td className="pr-3 text-right font-mono text-[12px] text-ink-500 tabular-nums @max-[34rem]:hidden">{item.size === null ? '—' : t.folders.size(item.size)}</td>
                  <td className="truncate pr-2 text-right font-mono text-[12px] text-ink-500 tabular-nums @max-[34rem]:hidden">{t.folders.time(item.modifiedAt)}</td>
                  <td className="pr-2"><EntryMenu entry={item} actions={actions} className="opacity-0 group-hover:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100" /></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {list.hasNextPage && (
          <button type="button" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage} className="mx-3 my-2 self-start text-[12.5px] font-medium text-ink hover:underline [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-riso-blue">
            {list.isFetchingNextPage ? t.folders.loading : t.folders.showMore}
          </button>
        )}
        {entry && <EntryPreview source={source} entry={entry} onEdit={() => onEditEntry(entry)} />}
      </div>
      <ScrollRuler targetRef={scrollRef} />
    </div>
  )
}

function Pending() {
  return (
    <p className="flex items-center gap-2 px-4 py-6 text-[13px] text-ink-500">
      <LoaderCircleIcon aria-hidden="true" className="riso-spin size-4 motion-reduce:animate-none" />
      {t.folders.loading}
    </p>
  )
}

// The root has no AGENTS.md yet: compact single-row banner offering one-click creation.
function EntryMissing({ onCreate }: { onCreate: () => void }) {
  return (
    <div role="note" className="mx-3 mt-3 mb-1 flex flex-wrap items-center justify-between gap-x-3 gap-y-1.5 rounded-card border border-hairline-strong bg-surface-subtle px-3 py-2 shadow-surface [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-tint-pink-2 [[data-skin=plate]_&]:shadow-print-pink">
      <div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2 gap-y-0.5 text-[12.5px]">
        <span className="shrink-0 font-semibold text-ink [[data-skin=plate]_&]:font-black">{t.folders.entry.missingTitle}</span>
        <span className="min-w-0 text-ink-500 [[data-skin=plate]_&]:text-ink-700">{t.folders.entry.missingBody}</span>
      </div>
      <Button size="xs" className="h-7 shrink-0" onClick={onCreate}>{t.folders.entry.create}</Button>
    </div>
  )
}

// AGENTS.md below the root listing: its content is shown directly, like the README on a code repository's home page.
function EntryPreview({ source, entry, onEdit }: { source: FolderSource; entry: FolderEntry; onEdit: () => void }) {
  const file = useFolderFile(source, entry.path)
  return (
    <section aria-label={ENTRY_FILE} className="mx-3 my-3 rounded-card border border-hairline bg-surface-control [[data-skin=plate]_&]:border-ink/12 [[data-skin=plate]_&]:bg-white">
      <header className="flex min-h-9 flex-wrap items-center gap-x-2 gap-y-1 border-b border-hairline px-3 py-1 [[data-skin=plate]_&]:border-ink/10">
        <BookOpenTextIcon aria-hidden="true" className="size-4 text-pink-text" strokeWidth={2} />
        <span className="text-[13px] font-semibold [[data-skin=plate]_&]:font-black">{ENTRY_FILE}</span>
        <EntryBadge />
        <span className="text-[12px] text-ink-500">{t.folders.entry.hint}</span>
        <ToolButton className="ml-auto" icon={<PencilIcon />} label={t.folders.file.edit} onClick={onEdit} />
      </header>
      <div className="px-5 py-3.5 text-[14px]">
        {file.data?.kind === 'text' ? <Markdown text={file.data.text} leadLabels={false} /> : file.isPending ? <Pending /> : <ErrorAlert error={file.error} />}
      </div>
    </section>
  )
}

// ---------- File ----------

export interface EditState {
  path: string
  original: string
  text: string
  // The original BOM and line endings, written back as they were on save.
  format: TextFormat
  saving: boolean
  error: unknown
}

interface FileViewProps {
  source: FolderSource
  entry: FolderEntry
  limits: TextEditLimits | undefined
  actions: EntryActions
  file: ReturnType<typeof useFolderFile>
  edit: EditState | null
  mode: 'preview' | 'source'
  onDraft: (text: string) => void
  onSave: () => void
}

export function FileView({ source, entry, limits, actions, file, edit, mode, onDraft, onSave }: FileViewProps) {
  const path = entry.path
  const maxBytes = limits?.maxBytes ?? 1 << 20
  const tooLarge = entry.size !== null && entry.size > maxBytes
  const text = file.data?.kind === 'text' ? file.data.text : null
  const markdown = isMarkdown(path)
  const editing = edit?.path === path ? edit : null

  let body: ReactNode
  if (editing) body = <Editor key={path} value={editing.text} onChange={onDraft} onSave={onSave} label={nameOf(path)} />
  else if (entry.type === 'other') body = <Notice>{t.folders.file.other}</Notice>
  else if (tooLarge) body = <Notice download={() => actions.download(entry)}>{t.folders.file.tooLarge(maxBytes)}</Notice>
  else if (entry.type === 'file' && isImage(path)) {
    body = (
      <div className="flex min-h-0 flex-1 items-center justify-center overflow-auto bg-[repeating-conic-gradient(var(--surface-subtle)_0_25%,var(--surface-control)_0_50%)] bg-[length:16px_16px] p-6 [[data-skin=plate]_&]:bg-[repeating-conic-gradient(var(--paper)_0_25%,#fff_0_50%)]">
        <img src={source.contentURL(path)} alt={entry.name} className="max-h-full max-w-full rounded-stamp shadow-surface [[data-skin=plate]_&]:shadow-print-rest" />
      </div>
    )
  } else if (file.isPending) body = <Pending />
  else if (file.error instanceof ApiError && file.error.status === 422 && entry.symlinkTarget && linksOutside(path, entry.symlinkTarget)) {
    body = <Notice>{t.folders.file.symlinkOutside}</Notice>
  } else if (file.isError) body = <div className="p-4"><ErrorAlert error={file.error} /></div>
  else if (file.data?.kind === 'binary') body = <Notice download={() => actions.download(entry)}>{t.folders.file.binary}</Notice>
  else if (text !== null && markdown && mode === 'preview') body = <MarkdownPreview key={path} text={text} />
  else if (text !== null) body = <CodeView key={path} path={path} text={text} />

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {editing?.error != null && <ErrorAlert error={editing.error} className="mx-3 mt-3 w-auto" />}
      {body}
    </div>
  )
}

function Notice({ children, download }: { children: ReactNode; download?: () => void }) {
  return (
    <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 p-8 text-center text-[13.5px] text-ink-700">
      <p>{children}</p>
      {download && <Button size="xs" variant="secondary" onClick={download}><DownloadIcon aria-hidden="true" />{t.folders.download}</Button>}
    </div>
  )
}

function MarkdownPreview({ text }: { text: string }) {
  const scrollRef = useRef<HTMLDivElement>(null)
  return (
    <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden">
      <div ref={scrollRef} className="min-h-0 flex-1 overflow-y-auto px-6 py-5 text-[14.5px]">
        <Markdown text={text} leadLabels={false} />
      </div>
      <ScrollRuler targetRef={scrollRef} />
    </div>
  )
}

// Read-only code view: line numbers plus highlighting, no wrapping, long lines scroll horizontally.
function CodeView({ path, text }: { path: string; text: string }) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const html = useMemo(() => highlightCode(text, languageOf(path)), [text, path])
  const count = Math.max(1, text.split('\n').length - (text.endsWith('\n') ? 1 : 0))
  return (
    <div className="relative flex min-h-0 flex-1 overflow-hidden">
      <div ref={scrollRef} className="code-view flex min-h-0 flex-1 overflow-auto bg-surface-control font-mono text-[12.5px] leading-5 [[data-skin=plate]_&]:bg-white">
        <div aria-hidden="true" className="sticky left-0 shrink-0 border-r border-hairline bg-surface-subtle py-3 pr-3 pl-4 text-right text-ink-500/70 tabular-nums select-none [[data-skin=plate]_&]:border-ink/10 [[data-skin=plate]_&]:bg-paper-raised">
          {Array.from({ length: count }, (_, index) => <div key={index}>{index + 1}</div>)}
        </div>
        <pre className="min-w-0 flex-1 py-3 pr-6 pl-4"><code className="hljs" dangerouslySetInnerHTML={{ __html: html }} /></pre>
      </div>
      <ScrollRuler targetRef={scrollRef} />
    </div>
  )
}

// Online editing: a monospace text box; Tab inserts a tab character, Cmd+S / Ctrl+S saves.
function Editor({ value, onChange, onSave, label }: { value: string; onChange: (text: string) => void; onSave: () => void; label: string }) {
  const scrollRef = useRef<HTMLTextAreaElement>(null)
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 's') { event.preventDefault(); onSave() }
    else if (event.key === 'Tab' && !event.shiftKey) {
      event.preventDefault()
      const el = event.currentTarget, start = el.selectionStart, end = el.selectionEnd
      onChange(value.slice(0, start) + '\t' + value.slice(end))
      requestAnimationFrame(() => el.setSelectionRange(start + 1, start + 1))
    }
  }
  return (
    <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden">
      <textarea
        ref={scrollRef}
        autoFocus
        aria-label={label}
        spellCheck={false}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={onKeyDown}
        className="min-h-0 flex-1 resize-none border-0 bg-surface-control px-5 py-3 font-mono text-[12.5px] leading-5 text-ink outline-none [tab-size:4] focus-visible:shadow-none [[data-skin=plate]_&]:bg-white"
      />
      <ScrollRuler targetRef={scrollRef} />
    </div>
  )
}
