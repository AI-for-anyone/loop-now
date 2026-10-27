import { useMemo, type KeyboardEvent, type ReactNode } from 'react'
import { BookOpenTextIcon, DownloadIcon, LoaderCircleIcon, PencilIcon } from 'lucide-react'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Markdown } from '@/features/chat/Markdown'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { canEdit, ENTRY_FILE, isEntryFile, isImage, isMarkdown, languageOf, linksOutside, nameOf, type FolderEntry, type FolderSource, type TextEditLimits, type TextFormat } from './folder-source'
import { useFolderFile, useFolderList } from './folder-queries'
import { EntryBadge, EntryIcon, EntryMenu, ToolButton, type EntryActions } from './FolderParts'
import { highlightCode } from './highlight'

// ---------- 文件夹 ----------

interface DirViewProps {
  source: FolderSource
  path: string
  title: string
  actions: EntryActions
  onCreateEntry: () => void
  onEditEntry: (entry: FolderEntry) => void
}

// 文件夹的内容列表（名称、大小、修改时间）；根目录下面接着显示 AGENTS.md，没有时提示创建。
export function DirView({ source, path, title, actions, onCreateEntry, onEditEntry }: DirViewProps) {
  const list = useFolderList(source, path)
  const entries = list.data?.pages.flatMap((page) => page.entries) ?? []
  const entry = path === '' ? entries.find((item) => item.name === ENTRY_FILE && item.type === 'file') : undefined
  const dirs = entries.filter((item) => item.type === 'dir').length
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
      {path === '' && list.isSuccess && !entry && <EntryMissing onCreate={onCreateEntry} />}
      <div className="flex items-baseline gap-2.5 px-4 pt-3.5 pb-2">
        <h3 className="min-w-0 truncate text-[15px] font-black">{title}</h3>
        {list.isSuccess && <span className="shrink-0 text-[12px] text-ink-500">{list.hasNextPage ? t.folders.partial(entries.length) : t.folders.summary(dirs, entries.length - dirs)}</span>}
      </div>
      <ErrorAlert error={list.error} className="mx-4 w-auto" />
      {list.isPending && <Pending />}
      {list.isSuccess && entries.length === 0 && <p className="px-4 py-8 text-center text-[13px] text-ink-500">{t.folders.empty}<br />{t.folders.emptyHint}</p>}
      {entries.length > 0 && (
        <table className="w-full table-fixed border-collapse text-[13px]">
          <thead className="sticky top-0 z-10 bg-white/95 text-[11.5px] font-black text-ink-500 backdrop-blur-sm">
            <tr className="border-y border-ink/10">
              <th className="py-1.5 pl-4 text-left font-black">{t.folders.columns.name}</th>
              <th className="w-24 pr-3 text-right font-black @max-[34rem]:hidden">{t.folders.columns.size}</th>
              <th className="w-28 pr-2 text-left font-black @max-[34rem]:hidden">{t.folders.columns.modified}</th>
              <th className="w-9"><span className="sr-only">{t.folders.actions('')}</span></th>
            </tr>
          </thead>
          <tbody>
            {[...entries].sort((a, b) => Number(isEntryFile(b.path)) - Number(isEntryFile(a.path))).map((item) => (
              <tr key={item.path} className="group h-8 border-b border-ink/[0.06] hover:bg-ink/[0.03]">
                <td className="py-0 pl-4">
                  <button type="button" onClick={() => actions.open(item)} className={cn('flex w-full min-w-0 cursor-pointer items-center gap-2 text-left outline-none focus-visible:underline', item.type === 'other' && 'text-ink-500')}>
                    <EntryIcon path={item.path} type={item.type} />
                    <span className={cn('min-w-0 truncate group-hover:text-riso-blue group-hover:underline', item.symlinkTarget && 'max-w-[60%] shrink-0', isEntryFile(item.path) && 'font-bold')}>{item.name}</span>
                    {item.symlinkTarget && <span className="min-w-0 truncate text-[12px] text-ink-500">→ {item.symlinkTarget}</span>}
                    {isEntryFile(item.path) && <EntryBadge />}
                  </button>
                </td>
                <td className="pr-3 text-right text-[12px] text-ink-500 tabular-nums @max-[34rem]:hidden">{item.size === null ? '—' : t.folders.size(item.size)}</td>
                <td className="truncate pr-2 text-[12px] text-ink-500 tabular-nums @max-[34rem]:hidden">{t.folders.time(item.modifiedAt)}</td>
                <td className="pr-2"><EntryMenu entry={item} actions={actions} className="opacity-0 group-hover:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100" /></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {list.hasNextPage && (
        <button type="button" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage} className="mx-4 my-2 self-start text-[12.5px] font-bold text-riso-blue hover:underline">
          {list.isFetchingNextPage ? t.folders.loading : t.folders.showMore}
        </button>
      )}
      {entry && <EntryPreview source={source} entry={entry} onEdit={() => onEditEntry(entry)} />}
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

// 根目录还没有 AGENTS.md：说明它的作用，一键创建。
function EntryMissing({ onCreate }: { onCreate: () => void }) {
  return (
    <div role="note" className="mx-4 mt-4 flex flex-wrap items-center gap-x-3.5 gap-y-2 rounded-card border-[1.5px] border-dashed border-riso-pink/60 bg-tint-pink-2/60 px-4 py-3">
      <BookOpenTextIcon aria-hidden="true" className="size-5 shrink-0 text-pink-text" strokeWidth={2} />
      <div className="min-w-0 flex-[1_1_240px]">
        <p className="text-[14px] font-black">{t.folders.entry.missingTitle}</p>
        <p className="text-[12.5px] leading-snug text-ink-700">{t.folders.entry.missingBody}</p>
      </div>
      <Button size="xs" onClick={onCreate}>{t.folders.entry.create}</Button>
    </div>
  )
}

// 根目录列表下面的 AGENTS.md：像代码仓库首页的 README 一样直接显示内容。
function EntryPreview({ source, entry, onEdit }: { source: FolderSource; entry: FolderEntry; onEdit: () => void }) {
  const file = useFolderFile(source, entry.path)
  return (
    <section aria-label={ENTRY_FILE} className="mx-4 mt-5 mb-4 rounded-card border border-ink/12 bg-white">
      <header className="flex min-h-10 flex-wrap items-center gap-x-2 gap-y-1 border-b border-ink/10 px-4 py-1.5">
        <BookOpenTextIcon aria-hidden="true" className="size-4 text-pink-text" strokeWidth={2} />
        <span className="text-[13.5px] font-black">{ENTRY_FILE}</span>
        <EntryBadge />
        <span className="text-[12px] text-ink-500">{t.folders.entry.hint}</span>
        <ToolButton className="ml-auto" icon={<PencilIcon />} label={t.folders.file.edit} onClick={onEdit} />
      </header>
      <div className="px-6 py-4 text-[14.5px]">
        {file.data?.kind === 'text' ? <Markdown text={file.data.text} /> : file.isPending ? <Pending /> : <ErrorAlert error={file.error} />}
      </div>
    </section>
  )
}

// ---------- 文件 ----------

export interface EditState {
  path: string
  original: string
  text: string
  // 原有的 BOM 与换行，保存时照原样写回。
  format: TextFormat
  saving: boolean
  error: unknown
}

interface FileViewProps {
  source: FolderSource
  entry: FolderEntry
  limits: TextEditLimits | undefined
  actions: EntryActions
  edit: EditState | null
  mode: 'preview' | 'source'
  onMode: (mode: 'preview' | 'source') => void
  onEdit: (text: string, format: TextFormat) => void
  onDraft: (text: string) => void
  onSave: () => void
  onCancel: () => void
}

export function FileView({ source, entry, limits, actions, edit, mode, onMode, onEdit, onDraft, onSave, onCancel }: FileViewProps) {
  const path = entry.path
  const maxBytes = limits?.maxBytes ?? 1 << 20
  const tooLarge = entry.size !== null && entry.size > maxBytes
  const readable = entry.type === 'file' && !tooLarge && !isImage(path)
  const file = useFolderFile(source, path, readable || (entry.type === 'symlink' && !!entry.symlinkTarget))
  const text = file.data?.kind === 'text' ? file.data.text : null
  const editable = entry.type === 'file' && canEdit(path, entry.size, limits) && text !== null
  const markdown = isMarkdown(path)
  const editing = edit?.path === path ? edit : null
  const dirty = !!editing && editing.text !== editing.original
  const lines = text === null ? null : text.split('\n').length - (text.endsWith('\n') ? 1 : 0)

  let body: ReactNode
  if (editing) body = <Editor value={editing.text} onChange={onDraft} onSave={onSave} label={nameOf(path)} />
  else if (entry.type === 'other') body = <Notice>{t.folders.file.other}</Notice>
  else if (tooLarge) body = <Notice download={() => actions.download(entry)}>{t.folders.file.tooLarge(maxBytes)}</Notice>
  else if (entry.type === 'file' && isImage(path)) body = <div className="flex min-h-0 flex-1 items-center justify-center overflow-auto bg-[repeating-conic-gradient(var(--paper)_0_25%,#fff_0_50%)] bg-[length:16px_16px] p-6"><img src={source.contentURL(path)} alt={entry.name} className="max-h-full max-w-full rounded-stamp shadow-print-rest" /></div>
  else if (file.isPending) body = <Pending />
  else if (file.error instanceof ApiError && file.error.status === 422 && entry.symlinkTarget && linksOutside(path, entry.symlinkTarget)) body = <Notice>{t.folders.file.symlinkOutside}</Notice>
  else if (file.isError) body = <div className="p-4"><ErrorAlert error={file.error} /></div>
  else if (file.data?.kind === 'binary') body = <Notice download={() => actions.download(entry)}>{t.folders.file.binary}</Notice>
  else if (text !== null && markdown && mode === 'preview') body = <div className="min-h-0 flex-1 overflow-y-auto px-7 py-5 text-[14.5px]"><Markdown text={text} /></div>
  else if (text !== null) body = <CodeView path={path} text={text} />

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* 窄时名称独占一行，操作按钮换到下一行；宽时同一行，说明文字先截断。 */}
      <div className={cn('flex min-h-11 shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b border-ink/10 px-4 py-1.5 @min-[34rem]:flex-nowrap', editing && 'bg-tint-yellow/50')}>
        <EntryIcon path={path} type={entry.type} />
        <h3 className="min-w-0 shrink truncate text-[14px] font-black">{entry.name}</h3>
        {isEntryFile(path) && <EntryBadge />}
        {editing ? (
          <span className="inline-flex min-w-0 items-center gap-1.5 truncate text-[12px] font-bold whitespace-nowrap text-ink-700">
            {t.folders.file.editing}
            {dirty && <span className="inline-flex items-center gap-1 text-pink-text"><span aria-hidden="true" className="size-1.5 rounded-full bg-riso-pink" />{t.folders.file.unsaved}</span>}
          </span>
        ) : (
          <span className="min-w-0 flex-1 truncate text-[12px] whitespace-nowrap text-ink-500 tabular-nums @max-[34rem]:hidden">
            {entry.type === 'symlink' ? t.folders.file.symlink(entry.symlinkTarget ?? '') : [entry.size !== null && t.folders.size(entry.size), t.folders.time(entry.modifiedAt), lines !== null && t.folders.file.lines(lines)].filter(Boolean).join(' · ')}
          </span>
        )}
        <div className="ml-auto flex shrink-0 items-center gap-1 @max-[34rem]:basis-full @max-[34rem]:justify-end">
          {editing ? (
            <>
              <Button size="xs" variant="ghost" className="h-7 no-underline" onClick={onCancel} disabled={editing.saving}>{t.folders.file.cancel}</Button>
              <Button size="xs" className="h-7 px-3 shadow-[2px_2px_0_var(--riso-pink)]" onClick={onSave} disabled={editing.saving || !dirty} title={t.folders.file.saveKey}>{editing.saving ? t.folders.file.saving : t.folders.file.save}</Button>
            </>
          ) : (
            <>
              {markdown && text !== null && (
                <div role="radiogroup" aria-label={t.folders.file.view} className="mr-1 inline-flex rounded-full border border-ink/15 p-0.5">
                  {(['preview', 'source'] as const).map((value) => (
                    <button key={value} type="button" role="radio" aria-checked={mode === value} onClick={() => onMode(value)} className={cn('h-6 cursor-pointer rounded-full px-2.5 text-[12px] font-bold text-ink-700', mode === value && 'bg-tint-blue text-ink')}>{value === 'preview' ? t.folders.file.preview : t.folders.file.source}</button>
                  ))}
                </div>
              )}
              {entry.type === 'file' && <ToolButton icon={<PencilIcon />} label={t.folders.file.edit} onClick={() => file.data?.kind === 'text' && onEdit(file.data.text, file.data.format)} disabled={!editable} title={editable ? t.folders.file.edit : t.folders.file.notEditable} />}
              {entry.type === 'file' && <ToolButton icon={<DownloadIcon />} label={t.folders.download} showLabel={false} onClick={() => actions.download(entry)} />}
              <EntryMenu entry={entry} actions={actions} />
            </>
          )}
        </div>
      </div>
      {editing?.error != null && <ErrorAlert error={editing.error} className="mx-4 mt-3 w-auto" />}
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

// 只读的代码视图：行号加高亮，不折行，长行横向滚动。
function CodeView({ path, text }: { path: string; text: string }) {
  const html = useMemo(() => highlightCode(text, languageOf(path)), [text, path])
  const count = Math.max(1, text.split('\n').length - (text.endsWith('\n') ? 1 : 0))
  return (
    <div className="code-view flex min-h-0 flex-1 overflow-auto bg-white font-mono text-[12.5px] leading-5">
      <div aria-hidden="true" className="sticky left-0 shrink-0 border-r border-ink/10 bg-paper-raised py-3 pr-3 pl-4 text-right text-ink-500/70 tabular-nums select-none">
        {Array.from({ length: count }, (_, index) => <div key={index}>{index + 1}</div>)}
      </div>
      <pre className="min-w-0 flex-1 py-3 pr-6 pl-4"><code className="hljs" dangerouslySetInnerHTML={{ __html: html }} /></pre>
    </div>
  )
}

// 在线编辑：等宽文本框，Tab 插入制表符，⌘S／Ctrl+S 保存。
function Editor({ value, onChange, onSave, label }: { value: string; onChange: (text: string) => void; onSave: () => void; label: string }) {
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
    <textarea
      autoFocus
      aria-label={label}
      spellCheck={false}
      value={value}
      onChange={(event) => onChange(event.target.value)}
      onKeyDown={onKeyDown}
      className="min-h-0 flex-1 resize-none border-0 bg-white px-5 py-3 font-mono text-[12.5px] leading-5 text-ink outline-none [tab-size:4] focus-visible:shadow-none"
    />
  )
}
