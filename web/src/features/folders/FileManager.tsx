import { useCallback, useEffect, useRef, useState, type ChangeEvent, type DragEvent, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { CircleAlertIcon, CircleCheckIcon, FilePlusIcon, FolderGit2Icon, FolderPlusIcon, InfoIcon, LoaderCircleIcon, PanelLeftIcon, RefreshCwIcon, XIcon } from 'lucide-react'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { RulerTicks, Stub, StubFigure } from '@/components/Sheet'
import { t } from '@/i18n'
import { config } from '@/config'
import { pickedFiles } from '@/lib/upload'
import { cn } from '@/lib/utils'
import { ancestorsOf, encodeText, ENTRY_FILE, folderKeys, joinPath, nameOf, parentOf, PLAIN_TEXT, segmentsOf, within, type FolderEntry, type FolderSource, type GitPull, type TextEditLimits, type TextFormat, type UploadItem } from './folder-source'
import { refreshDirs, useFolderList, useGitPulls, type FileText, type Selection } from './folder-queries'
import { DirView, FileView, type EditState } from './FolderContent'
import { GitPullDialog, MoveDialog } from './FolderDialogs'
import { GitPullHint } from '@/features/local-mode/GitPullHint'
import { FileTree, type Naming } from './FileTree'
import { FolderBlock, ToolButton, UploadMenu, type EntryActions } from './FolderParts'
import { droppedItems, useFolderUploads, type UploadRow } from './use-folder-uploads'

interface FileManagerProps {
  source: FolderSource
  // The root's name in the tree and the path bar, e.g. “server 的工作区” (server's workspace).
  rootLabel: string
  // Context.limits.textEdit: the extensions and size limit for online editing.
  limits: TextEditLimits | undefined
  // Context.limits.maxFolderUpload: the limit for a single uploaded file.
  maxUpload?: number
  className?: string
  // card: a white card with a light border (global settings); sheet: the Agent's folder tab and the project folder page, laid directly
  // on the sheet, with the overview in the stub on the right;
  // panel: a block inside one section of a sheet (project settings), with the same toolbar and list as sheet, a 2px navy outline around
  // it and no stub.
  variant?: 'card' | 'sheet' | 'panel'
  // The first sentence of the note at the bottom of the sheet variant (e.g. “这是它的工作目录” (This is its working directory)).
  note?: ReactNode
}

type Dialog =
  | { type: 'remove'; entry: FolderEntry }
  | { type: 'move'; entry: FolderEntry }
  | { type: 'git' }
  // Leaving while an edit has unsaved changes: after confirming, go to next, or just exit editing.
  | { type: 'discard'; next: Selection | null }

const ROOT: Selection = { path: '', type: 'dir', entry: null }
const INTERNAL = 'application/x-loop-now-path'

// File manager: the file tree, expanded level by level, on the left; the folder list or file content on the right. When narrow, only the
// right side shows and the file tree slides out from the left.
export function FileManager({ source, rootLabel, limits, maxUpload, className, variant = 'card', note }: FileManagerProps) {
  // sheet and panel use the redesigned toolbar (pills with labels), a dashed line and a note at the bottom; only sheet has a stub.
  const sheet = variant !== 'card'
  const client = useQueryClient()
  const [selection, setSelection] = useState<Selection>(ROOT)
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set())
  const [naming, setNaming] = useState<Naming | null>(null)
  const [edit, setEdit] = useState<EditState | null>(null)
  const [mode, setMode] = useState<'preview' | 'source'>('preview')
  const [dialog, setDialog] = useState<Dialog | null>(null)
  const [treeOpen, setTreeOpen] = useState(false)
  const [dropTarget, setDropTarget] = useState<string | null>(null)
  const [draggingFiles, setDraggingFiles] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [dismissed, setDismissed] = useState<ReadonlySet<string>>(() => new Set())
  const [pullDetails, setPullDetails] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null), folderInput = useRef<HTMLInputElement>(null), uploadTarget = useRef('')
  const uploads = useFolderUploads(source, maxUpload, useCallback((dirs: string[]) => void refreshDirs(client, source, ...dirs), [client, source]))
  const pulls = useGitPulls(source)
  const currentDir = selection.type === 'dir' ? selection.path : parentOf(selection.path)
  const dirty = !!edit && edit.text !== edit.original

  // Pulls that ran while the page was open and then succeeded: re-read the level containing the target folder and tell the person in the
  // status bar.
  const active = useRef(new Set<string>()), [finished, setFinished] = useState<ReadonlySet<string>>(() => new Set())
  useEffect(() => {
    for (const item of pulls.data ?? []) {
      if (item.status === 'queued' || item.status === 'running') active.current.add(item.id)
      else if (item.status === 'succeeded' && active.current.delete(item.id)) {
        void refreshDirs(client, source, parentOf(item.path), item.path)
        setFinished((old) => new Set([...old, item.id]))
      }
    }
  }, [client, source, pulls.data])
  // The one pull shown in the status bar: an in-progress pull comes first; otherwise the most recent one, if it failed (still shown after a
  // reload) or succeeded on this page, until it is dismissed.
  const latest = pulls.data?.[0]
  const pull: GitPull | undefined = pulls.data?.find((item) => item.status === 'queued' || item.status === 'running')
    ?? (latest && !dismissed.has(latest.id) && (latest.status === 'failed' || finished.has(latest.id)) ? latest : undefined)

  const go = (next: Selection, expandSelf = true) => {
    if (dirty) { setDialog({ type: 'discard', next }); return }
    // The file tree drawer on narrow screens: clicking a folder keeps expanding it inside the drawer; opening a file closes the drawer.
    setEdit(null); setSelection(next); setError(null)
    if (next.type !== 'dir') setTreeOpen(false)
    if (next.type === 'file' || next.type === 'symlink') setMode('preview')
    setExpanded((old) => new Set([...old, ...ancestorsOf(next.path), ...(expandSelf && next.type === 'dir' && next.path ? [next.path] : [])]))
  }
  const toggle = (path: string) => setExpanded((old) => { const next = new Set(old); if (next.has(path)) next.delete(path); else next.add(path); return next })
  // A folder was deleted or moved away: drop the cache for it and every level inside it.
  const forget = (path: string) => client.removeQueries({ predicate: (query) => query.queryKey[0] === 'folder' && query.queryKey[1] === source.key && typeof query.queryKey[3] === 'string' && within(query.queryKey[3], path) })
  const run = async (work: () => Promise<unknown>) => { setError(null); try { await work() } catch (caught) { setError(caught); throw caught } }

  const startEdit = (path: string, text: string, format: TextFormat) => setEdit({ path, original: text, text, format, saving: false, error: null })
  const save = async () => {
    if (!edit || edit.saving) return
    const { path, text, format } = edit
    setEdit({ ...edit, saving: true, error: null })
    try {
      const entry = await source.write(path, encodeText(text, format), false)
      const saved: FileText = { kind: 'text', text, format }
      client.setQueryData(folderKeys.file(source.key, path), saved)
      void refreshDirs(client, source, parentOf(path))
      setEdit(null)
      setSelection((old) => (old.path === path ? { ...old, entry } : old))
    } catch (caught) { setEdit((old) => (old ? { ...old, saving: false, error: caught } : old)) }
  }
  const createEntryFile = () => run(async () => {
    const entry = await source.write(ENTRY_FILE, t.folders.entry.template, true)
    await refreshDirs(client, source, '')
    go({ path: ENTRY_FILE, type: 'file', entry })
    startEdit(ENTRY_FILE, t.folders.entry.template, PLAIN_TEXT)
  })

  const actions: EntryActions = {
    open: (entry) => go(entry ? { path: entry.path, type: entry.type, entry } : ROOT),
    create: (dir, type) => { go({ path: dir, type: 'dir', entry: null }); setNaming({ mode: 'create', dir, type }) },
    upload: (dir) => { uploadTarget.current = dir; fileInput.current?.click() },
    rename: (entry) => setNaming({ mode: 'rename', entry }),
    move: (entry) => setDialog({ type: 'move', entry }),
    download: (entry) => { const a = document.createElement('a'); a.href = source.contentURL(entry.path); a.download = entry.name; a.click() },
    remove: (entry) => setDialog({ type: 'remove', entry }),
  }

  const name = (value: string) => {
    const current = naming
    setNaming(null)
    if (!current) return
    if (current.mode === 'create') {
      const path = joinPath(current.dir, value)
      void run(async () => {
        const entry = current.type === 'dir' ? await source.mkdir(path) : await source.write(path, '', true)
        await refreshDirs(client, source, current.dir)
        go({ path, type: entry.type, entry })
        if (current.type === 'file') startEdit(path, '', PLAIN_TEXT)
      }).catch(() => {})
    } else {
      const from = current.entry.path, to = joinPath(parentOf(from), value)
      void run(async () => {
        const entry = await source.move(from, to)
        forget(from)
        await refreshDirs(client, source, parentOf(from))
        if (within(selection.path, from)) setSelection({ path: to + selection.path.slice(from.length), type: selection.type, entry: selection.path === from ? entry : selection.entry })
      }).catch(() => {})
    }
  }

  const moveInto = async (from: string, dir: string) => {
    const to = joinPath(dir, nameOf(from))
    if (to === from || within(dir, from)) return
    await run(async () => {
      const entry = await source.move(from, to)
      forget(from)
      await refreshDirs(client, source, parentOf(from), dir)
      setExpanded((old) => new Set([...old, ...ancestorsOf(to)]))
      if (within(selection.path, from)) setSelection({ path: to + selection.path.slice(from.length), type: selection.type, entry: selection.path === from ? entry : selection.entry })
    })
  }

  const onDropOn = (dir: string, event: DragEvent) => {
    event.preventDefault()
    setDropTarget(null); setDraggingFiles(false)
    const from = event.dataTransfer.getData(INTERNAL)
    if (from) { void moveInto(from, dir).catch(() => {}); return }
    void droppedItems(event.dataTransfer).then((items) => { if (items.length) void uploads.start(dir, items) })
  }
  const onPick = (event: ChangeEvent<HTMLInputElement>) => {
    const items: UploadItem[] = event.target.files ? pickedFiles(event.target.files) : []
    event.target.value = ''
    if (items.length) void uploads.start(uploadTarget.current, items)
  }

  const tree = (
    <FileTree
      rootLabel={rootLabel}
      source={source}
      expanded={expanded}
      selected={selection.path}
      naming={naming}
      dropTarget={dropTarget}
      actions={actions}
      onToggle={toggle}
      onSelect={(entry) => go({ path: entry.path, type: entry.type, entry }, false)}
      onName={name}
      onNameCancel={() => setNaming(null)}
      onDragTarget={setDropTarget}
      onDropOn={onDropOn}
    />
  )

  const manager = (
    <div
      data-file-manager
      className={cn(
        '@container relative flex min-h-0 flex-col overflow-hidden text-ink',
        variant === 'sheet'
          ? 'min-w-0 flex-1 gap-5 pt-4 pr-[clamp(14px,3vw,40px)] pb-[22px] pl-[clamp(12px,2vw,34px)]'
          : variant === 'panel'
            ? cn('gap-4 rounded-card border-2 border-ink bg-paper-raised px-4 pt-4 pb-3', className)
            : cn('rounded-card border-[1.5px] border-ink/15 bg-white', className),
      )}
      onDragOver={(event) => { if (event.dataTransfer.types.includes('Files') || event.dataTransfer.types.includes(INTERNAL)) { event.preventDefault(); if (event.dataTransfer.types.includes('Files')) setDraggingFiles(true) } }}
      onDragLeave={(event) => { if (!(event.relatedTarget instanceof Node) || !event.currentTarget.contains(event.relatedTarget)) { setDraggingFiles(false); setDropTarget(null) } }}
      onDrop={(event) => onDropOn(dropTarget ?? currentDir, event)}
    >
      <input ref={fileInput} type="file" multiple className="sr-only" tabIndex={-1} aria-hidden="true" onChange={onPick} />
      <input ref={folderInput} type="file" multiple className="sr-only" tabIndex={-1} aria-hidden="true" onChange={onPick} {...{ webkitdirectory: '' }} />

      {/* When narrow, the path bar takes a row of its own and the action buttons wrap to a second row. The sheet variant is a toolbar at the
          top of the sheet: no background color or divider, and the buttons are pills with labels. */}
      <div className={cn('flex shrink-0 flex-wrap items-center', sheet ? 'gap-x-4 gap-y-2.5' : 'min-h-11 gap-x-1.5 gap-y-1 border-b border-ink/10 bg-paper-raised px-2.5 py-1.5')}>
        <ToolButton className="@min-[40rem]:hidden" icon={<PanelLeftIcon />} label={treeOpen ? t.folders.hideTree : t.folders.showTree} showLabel={false} aria-expanded={treeOpen} onClick={() => setTreeOpen((value) => !value)} />
        <Breadcrumbs className="@max-[34rem]:basis-[calc(100%-2.25rem)]" rootLabel={rootLabel} path={selection.path} onNavigate={(path) => go(path ? { path, type: 'dir', entry: null } : ROOT)} />
        <div className={cn('ml-auto flex shrink-0 flex-wrap items-center', sheet ? 'gap-2' : 'gap-0.5')}>
          <ToolButton pill={sheet} className={cn(sheet && '@max-[40rem]:[&>span]:hidden')} icon={<FilePlusIcon />} label={t.folders.newFile} showLabel={sheet} onClick={() => actions.create(currentDir, 'file')} />
          <ToolButton pill={sheet} className={cn(sheet && '@max-[40rem]:[&>span]:hidden')} icon={<FolderPlusIcon />} label={t.folders.newFolder} showLabel={sheet} onClick={() => actions.create(currentDir, 'dir')} />
          <ToolButton pill={sheet} icon={<RefreshCwIcon />} label={t.folders.refresh} showLabel={false} onClick={() => void client.invalidateQueries({ queryKey: folderKeys.all(source.key) })} />
          {!sheet && <span aria-hidden="true" className="mx-1 h-4 w-px bg-ink/15" />}
          <UploadMenu pill={sheet} className="@max-[34rem]:[&>span]:hidden" onFiles={() => actions.upload(currentDir)} onFolder={() => { uploadTarget.current = currentDir; folderInput.current?.click() }} />
          {source.git && <ToolButton pill={sheet} className="@max-[34rem]:[&>span]:hidden" icon={<FolderGit2Icon />} label={t.folders.gitPull} onClick={() => setDialog({ type: 'git' })} />}
        </div>
      </div>

      <div className="relative flex min-h-0 flex-1">
        <aside aria-label={t.folders.tree} className={cn('hidden w-64 shrink-0 overflow-y-auto @min-[40rem]:block', sheet ? 'mr-4 border-r-2 border-dashed border-ink/25 pr-2' : 'border-r border-ink/10 bg-paper-raised/60')}>{tree}</aside>
        {treeOpen && (
          <div className="absolute inset-0 z-20 flex @min-[40rem]:hidden">
            <aside aria-label={t.folders.tree} className="w-[min(18rem,85%)] overflow-y-auto border-r border-ink/15 bg-paper-raised shadow-[4px_0_0_rgb(24_38_94/.08)]">{tree}</aside>
            <button type="button" aria-label={t.folders.hideTree} className="flex-1 cursor-default bg-ink/20" onClick={() => setTreeOpen(false)} />
          </div>
        )}
        <section data-folder-content className="flex min-w-0 flex-1 flex-col" aria-label={t.folders.label}>
          {error != null && <ErrorAlert error={error} className="mx-4 mt-3 w-auto" />}
          {selection.type === 'dir' ? (
            <DirView
              source={source}
              path={selection.path}
              title={selection.path ? nameOf(selection.path) : rootLabel}
              actions={actions}
              onCreateEntry={() => void createEntryFile().catch(() => {})}
              onEditEntry={(entry) => {
                go({ path: entry.path, type: 'file', entry })
                const file = client.getQueryData<FileText>(folderKeys.file(source.key, entry.path))
                if (file?.kind === 'text') startEdit(entry.path, file.text, file.format)
              }}
            />
          ) : selection.entry ? (
            <FileView
              source={source}
              entry={selection.entry}
              limits={limits}
              actions={actions}
              edit={edit}
              mode={mode}
              onMode={setMode}
              onEdit={(text, format) => startEdit(selection.path, text, format)}
              onDraft={(text) => setEdit((old) => (old ? { ...old, text } : old))}
              onSave={() => void save()}
              onCancel={() => (dirty ? setDialog({ type: 'discard', next: null }) : setEdit(null))}
            />
          ) : null}
        </section>
      </div>

      {pull?.status === 'failed' && <GitPullHint pull={pull} />}
      {pull?.status === 'failed' && pullDetails && pull.errorMessage && (
        <pre className="max-h-32 shrink-0 overflow-auto border-t border-ink/10 bg-tint-red/40 px-4 py-2 font-mono text-[11.5px] leading-[1.6] whitespace-pre-wrap text-red-text">{pull.errorMessage}</pre>
      )}
      <div
        data-folder-footer
        className={cn(
          'flex shrink-0 items-center gap-3 text-ink-500',
          sheet ? 'min-h-9 flex-wrap border-t-2 border-dashed border-ink/[0.22] pt-3.5 text-[12px]' : 'min-h-8 border-t border-ink/10 bg-paper-raised px-3 text-[11.5px]',
        )}
      >
        {/* Narrow screens can't fit both parts: when there is a pull status, show only the status. */}
        <span className={cn('flex min-w-0 items-center gap-1.5', pull && '@max-[40rem]:hidden')}>
          {!sheet && <InfoIcon aria-hidden="true" className="size-3.5 shrink-0" />}
          <span className={cn(!sheet && 'truncate')}>
            {sheet && note && <>{note} </>}
            {t.folders.concurrent}
          </span>
        </span>
        {pull && <PullStatus pull={pull} details={pullDetails} onDetails={() => setPullDetails((value) => !value)} onShow={() => go({ path: pull.path, type: 'dir', entry: null })} onRetry={() => setDialog({ type: 'git' })} onDismiss={() => { setDismissed((old) => new Set([...old, pull.id])); setPullDetails(false) }} />}
      </div>

      {uploads.rows.length > 0 && <UploadPanel rows={uploads.rows} onClose={uploads.clear} />}
      {draggingFiles && (
        <div aria-hidden="true" className="pointer-events-none absolute inset-1.5 z-30 flex items-center justify-center rounded-[12px] border-2 border-dashed border-riso-blue bg-tint-blue/70">
          <span className="rounded-full bg-white px-4 py-2 text-[13.5px] font-black text-riso-blue shadow-print-rest">{t.folders.drop((dropTarget ?? currentDir) || rootLabel)}</span>
        </div>
      )}

      {dialog?.type === 'remove' && (
        <ConfirmDialog
          open
          onOpenChange={(open) => !open && setDialog(null)}
          destructive
          title={t.folders.removeDialog.title(dialog.entry.name)}
          body={dialog.entry.type === 'dir' ? t.folders.removeDialog.folder : t.folders.removeDialog.file}
          confirmLabel={t.folders.removeDialog.confirm}
          onConfirm={async () => {
            const { path } = dialog.entry
            await source.remove(path)
            forget(path)
            await refreshDirs(client, source, parentOf(path))
            if (within(selection.path, path)) { setEdit(null); setSelection(parentOf(path) ? { path: parentOf(path), type: 'dir', entry: null } : ROOT) }
          }}
        />
      )}
      {dialog?.type === 'move' && <MoveDialog entry={dialog.entry} onClose={() => setDialog(null)} onSubmit={(dir) => moveInto(dialog.entry.path, dir)} />}
      {dialog?.type === 'git' && source.git && <GitPullDialog onClose={() => setDialog(null)} onSubmit={async (input) => {
        // A pull started on this page is recorded as in progress right away, so its success still shows even if it finishes before the
        // next status read.
        const started = await source.git!.start(input)
        active.current.add(started.id)
        setPullDetails(false)
        await client.invalidateQueries({ queryKey: folderKeys.git(source.key) })
      }} />}
      {dialog?.type === 'discard' && (
        <ConfirmDialog
          open
          onOpenChange={(open) => !open && setDialog(null)}
          title={t.folders.file.discardTitle}
          body={t.folders.file.discardBody}
          confirmLabel={t.folders.file.discard}
          destructive
          onConfirm={() => { const next = dialog.next; setEdit(null); setDialog(null); if (next) { setSelection(next); setExpanded((old) => new Set([...old, ...ancestorsOf(next.path)])) } }}
        />
      )}
    </div>
  )
  if (variant !== 'sheet') return manager
  // Sheet variant: the overview in the stub on the right, hidden on narrow screens.
  return (
    <div className={cn('flex min-h-0', className)}>
      {manager}
      <FolderStub source={source} dir={currentDir} />
    </div>
  )
}

// The overview in the stub: the number of folders, the number of files and the total file size in the current folder. Only the pages
// read so far are counted; while not everything has been read, the numbers get a trailing "+".
function FolderStub({ source, dir }: { source: FolderSource; dir: string }) {
  const list = useFolderList(source, dir)
  const entries = list.data?.pages.flatMap((page) => page.entries) ?? []
  const more = list.hasNextPage ? '+' : ''
  const files = entries.filter((entry) => entry.type === 'file')
  const size = t.folders.size(files.reduce((sum, entry) => sum + (entry.size ?? 0), 0))
  const split = size.lastIndexOf(' ')
  return (
    <Stub as="aside" aria-label={t.folders.stub.title} label={t.folders.stub.title} className="w-[150px]">
      {list.isSuccess && (
        <div className="relative flex flex-1 flex-col gap-[22px] pt-1.5 pr-[30px] pl-[18px]">
          <RulerTicks />
          <StubFigure value={`${entries.filter((entry) => entry.type === 'dir').length}${more}`} label={t.folders.stub.dirs} />
          <StubFigure value={`${files.length}${more}`} label={t.folders.stub.files} />
          <StubFigure tone="ink" value={`${size.slice(0, split)}${more}`} label={size.slice(split + 1)} />
        </div>
      )}
    </Stub>
  )
}

// A Git pull in the status bar: in progress, succeeded (you can go and look), or failed (you can view the git output and retry).
function PullStatus({ pull, details, onDetails, onShow, onRetry, onDismiss }: { pull: GitPull; details: boolean; onDetails: () => void; onShow: () => void; onRetry: () => void; onDismiss: () => void }) {
  const ended = pull.status === 'succeeded' || pull.status === 'failed'
  return (
    <span role="status" className={cn('ml-auto flex min-w-0 shrink-0 items-center gap-1.5 font-bold', pull.status === 'failed' ? 'text-red-text' : pull.status === 'succeeded' ? 'text-green-text' : 'text-ink-700')}>
      {pull.status === 'failed' ? <CircleAlertIcon aria-hidden="true" className="size-3.5" /> : pull.status === 'succeeded' ? <CircleCheckIcon aria-hidden="true" className="size-3.5" /> : <LoaderCircleIcon aria-hidden="true" className="riso-spin size-3.5 motion-reduce:animate-none" />}
      <span className="max-w-[24rem] truncate" title={pull.url}>
        {pull.status === 'queued' ? t.folders.git.queued(pull.url, pull.path) : pull.status === 'running' ? t.folders.git.running(pull.url, pull.path) : pull.status === 'succeeded' ? t.folders.git.succeeded(pull.path) : t.folders.git.failed(pull.errorCode ? t.folders.git.errors[pull.errorCode] : t.folders.git.errors.clone_failed)}
      </span>
      {pull.status === 'succeeded' && <button type="button" className="cursor-pointer text-riso-blue underline" onClick={onShow}>{t.folders.git.show}</button>}
      {pull.status === 'failed' && pull.errorMessage && <button type="button" aria-expanded={details} className="cursor-pointer underline" onClick={onDetails}>{t.folders.git.details}</button>}
      {pull.status === 'failed' && <button type="button" className="cursor-pointer text-riso-blue underline" onClick={onRetry}>{t.folders.git.retry}</button>}
      {ended && <button type="button" aria-label={t.folders.git.dismiss} className="cursor-pointer rounded-full p-0.5 hover:bg-tint-blue" onClick={onDismiss}><XIcon aria-hidden="true" className="size-3.5" /></button>}
    </span>
  )
}

// Path bar: the root and every folder level can be clicked to go back to that level; the current item is bold.
function Breadcrumbs({ rootLabel, path, onNavigate, className }: { rootLabel: string; path: string; onNavigate: (path: string) => void; className?: string }) {
  const parts = segmentsOf(path)
  return (
    <nav aria-label={t.folders.path} className={cn('flex min-w-0 flex-1 items-center gap-0.5 overflow-hidden text-[13px]', className)}>
      <button type="button" onClick={() => onNavigate('')} className={cn('inline-flex min-w-0 shrink cursor-pointer items-center gap-1.5 rounded-[6px] px-1.5 py-0.5 hover:bg-tint-blue', parts.length === 0 ? 'font-black' : 'font-bold text-ink-700')} aria-current={parts.length === 0 ? 'page' : undefined}>
        <FolderBlock className="mr-1" />
        <span className="truncate">{rootLabel}</span>
      </button>
      {parts.map((part, index) => {
        const last = index === parts.length - 1, target = parts.slice(0, index + 1).join('/')
        return (
          <span key={target} className={cn('flex min-w-0 items-center gap-0.5', !last && 'shrink-[2]')}>
            <span aria-hidden="true" className="text-ink-500/60">/</span>
            {last ? <span aria-current="page" className="truncate px-1.5 font-black">{part}</span> : <button type="button" onClick={() => onNavigate(target)} className="cursor-pointer truncate rounded-[6px] px-1.5 py-0.5 font-bold text-ink-700 hover:bg-tint-blue">{part}</button>}
          </span>
        )
      })}
    </nav>
  )
}

// Upload progress: a small card in the bottom-right corner, listing each file. When all succeed it closes itself after a moment; if any
// failed it stays until the person closes it.
function UploadPanel({ rows, onClose }: { rows: UploadRow[]; onClose: () => void }) {
  const done = rows.filter((row) => row.state === 'done').length, failed = rows.filter((row) => row.state === 'failed').length
  const total = rows.reduce((sum, row) => sum + row.size, 0), loaded = rows.reduce((sum, row) => sum + row.loaded, 0)
  const finished = done + failed === rows.length
  useEffect(() => {
    if (!finished || failed > 0) return
    const timer = setTimeout(onClose, config.uploadPanelMs)
    return () => clearTimeout(timer)
  }, [finished, failed, onClose])
  return (
    <section aria-label={t.folders.upload} className="absolute right-3 bottom-11 z-20 w-[min(20rem,calc(100%-1.5rem))] rounded-card border border-ink/15 bg-paper-raised shadow-[3px_3px_0_rgb(24_38_94/.14)]">
      <header className="flex items-center gap-2 px-3.5 pt-2.5 pb-2">
        <h3 role="status" className="text-[13px] font-black">{t.folders.uploads.title(done, rows.length, finished)}</h3>
        {failed > 0 && <span className="text-[12px] font-bold text-red-text">{t.folders.uploads.failed(failed)}</span>}
        {finished && <button type="button" aria-label={t.folders.uploads.close} onClick={onClose} className="ml-auto cursor-pointer rounded-full p-1 text-ink-500 hover:bg-tint-blue"><XIcon aria-hidden="true" className="size-3.5" /></button>}
      </header>
      <div className="mx-3.5 h-1.5 overflow-hidden rounded-full bg-tint-blue"><div className={cn('h-full rounded-full', failed ? 'bg-riso-pink' : 'bg-riso-blue')} style={{ width: `${total ? Math.round((loaded / total) * 100) : 100}%` }} /></div>
      <ul className="mt-1.5 max-h-36 overflow-y-auto px-1.5 pb-1.5">
        {rows.map((row) => (
          <li key={row.id} className="flex h-7 items-center gap-2 rounded-[6px] px-2 text-[12.5px]" title={row.error ?? row.path}>
            <span className="min-w-0 flex-1 truncate">{row.path}</span>
            <span className={cn('shrink-0 tabular-nums', row.state === 'failed' ? 'font-bold text-red-text' : row.state === 'done' ? 'text-green-text' : 'text-ink-500')}>
              {row.state === 'done' ? <CircleCheckIcon aria-hidden="true" className="size-3.5" /> : row.state === 'failed' ? <CircleAlertIcon aria-hidden="true" className="size-3.5" /> : `${row.size ? Math.round((row.loaded / row.size) * 100) : 0}%`}
            </span>
          </li>
        ))}
      </ul>
    </section>
  )
}
