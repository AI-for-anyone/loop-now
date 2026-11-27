import { useCallback, useEffect, useMemo, useRef, useState, type ChangeEvent, type DragEvent, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { CircleAlertIcon, CircleCheckIcon, DownloadIcon, ExternalLinkIcon, FilePlusIcon, FolderGit2Icon, FolderPlusIcon, InfoIcon, LoaderCircleIcon, PanelLeftIcon, PencilIcon, RefreshCwIcon, XIcon } from 'lucide-react'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { config } from '@/config'
import { pickedFiles } from '@/lib/upload'
import { cn } from '@/lib/utils'
import { ancestorsOf, canEdit, encodeText, ENTRY_FILE, folderKeys, isEntryFile, isImage, isMarkdown, joinPath, nameOf, parentOf, PLAIN_TEXT, segmentsOf, within, type FolderEntry, type FolderSource, type GitPull, type TextEditLimits, type TextFormat, type UploadItem } from './folder-source'
import { refreshDirs, useFolderFile, useFolderList, useGitPulls, type FileText, type Selection } from './folder-queries'
import { DirView, FileView, Pending, type EditState } from './FolderContent'
import { GitPullDialog, MoveDialog } from './FolderDialogs'
import { GitPullHint } from '@/features/local-mode/GitPullHint'
import { FileTree, type Naming } from './FileTree'
import { EntryBadge, EntryIcon, EntryMenu, ToolButton, ToolLink, UploadMenu, type EntryActions } from './FolderParts'
import { droppedItems, useFolderUploads, type UploadRow } from './use-folder-uploads'

interface FileManagerProps {
  source: FolderSource
  // Relative path to open right away (e.g. from a chat link into the Agent's Folder tab); ancestors are expanded in the tree.
  initialPath?: string
  // The root's name in the tree and the path bar, e.g. "server's folder".
  rootLabel: string
  // Context.limits.textEdit: the extensions and size limit for online editing.
  limits: TextEditLimits | undefined
  // Context.limits.maxFolderUpload: the limit for a single uploaded file.
  maxUpload?: number
  className?: string
  // card: a bordered card (default); sheet: full-bleed 2-pane explorer on the Agent's folder tab and the project folder page;
  // panel: a bordered block inside a settings section.
  variant?: 'card' | 'sheet' | 'panel'
  // Optional context note surfaced in the status bar's info tooltip.
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

// File manager: full-bleed 2-pane IDE split with a collapsible file tree on the left, folder/file content on the right, a single
// 40px contextual toolbar at the top, and a 28px status strip at the bottom.
export function FileManager({ source, initialPath, rootLabel, limits, maxUpload, className, variant = 'card', note }: FileManagerProps) {
  const sheet = variant !== 'card'
  const client = useQueryClient()
  // User navigation overrides initialPath once clicked; while waiting to resolve initialPath we look it up in its parent directory.
  const [navSelection, setNavSelection] = useState<{ forInitial: string | undefined; value: Selection } | null>(null)
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set(initialPath ? ancestorsOf(initialPath) : []))
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

  const hasNav = navSelection !== null && navSelection.forInitial === initialPath
  const locating = !hasNav && Boolean(initialPath)
  const targetParent = locating && initialPath ? parentOf(initialPath) : ''
  const targetList = useFolderList(source, targetParent, locating)
  const targetFound = locating && initialPath ? targetList.data?.pages.flatMap((page) => page.entries).find((entry) => entry.path === initialPath) : undefined
  // If the target wasn't on the first page of a large parent folder, fetch subsequent pages until found or exhausted.
  useEffect(() => {
    if (locating && targetList.isSuccess && !targetFound && targetList.hasNextPage && !targetList.isFetchingNextPage) {
      void targetList.fetchNextPage()
    }
  }, [locating, targetFound, targetList])
  const locatingPending = locating && !targetFound && !targetList.isError && (!targetList.isSuccess || targetList.hasNextPage)
  const selection: Selection = hasNav
    ? navSelection.value
    : !initialPath
      ? ROOT
      : targetFound
        ? { path: targetFound.path, type: targetFound.type, entry: targetFound }
        : targetList.isSuccess && !targetList.hasNextPage
          ? (targetParent ? { path: targetParent, type: 'dir', entry: null } : ROOT)
          : ROOT
  const setSelection = (next: Selection | ((old: Selection) => Selection)) =>
    setNavSelection((prev) => {
      const current = prev && prev.forInitial === initialPath ? prev.value : selection
      return { forInitial: initialPath, value: typeof next === 'function' ? next(current) : next }
    })
  const effectiveExpanded = useMemo(() => {
    if (!locating || !initialPath) return expanded
    const extra = [...ancestorsOf(initialPath), ...(targetFound?.type === 'dir' ? [targetFound.path] : [])]
    return extra.every((item) => expanded.has(item)) ? expanded : new Set([...expanded, ...extra])
  }, [expanded, locating, initialPath, targetFound])
  const displayError = error ?? (locating && targetList.isError ? targetList.error : null)

  const currentDir = selection.type === 'dir' ? selection.path : parentOf(selection.path)
  const dirty = !!edit && edit.text !== edit.original

  const selectedEntry = selection.type !== 'dir' ? selection.entry : null
  const maxBytes = limits?.maxBytes ?? 20 << 20
  const fileTooLarge = !!selectedEntry && selectedEntry.size !== null && selectedEntry.size > maxBytes
  const fileReadable = !!selectedEntry && (
    (selectedEntry.type === 'file' && !fileTooLarge && !isImage(selectedEntry.path)) ||
    (selectedEntry.type === 'symlink' && !!selectedEntry.symlinkTarget)
  )
  const activeFile = useFolderFile(source, selectedEntry?.path ?? '', fileReadable)
  const activeText = selectedEntry && activeFile.data?.kind === 'text' ? activeFile.data.text : null
  const activeEditable = !!selectedEntry && selectedEntry.type === 'file' && canEdit(selectedEntry.path, selectedEntry.size, limits) && activeText !== null
  const activeOpenRawURL = selectedEntry && source.rawURL && (
    (selectedEntry.type === 'file' && !fileTooLarge) ||
    (selectedEntry.type === 'symlink' && activeFile.isSuccess)
  ) ? source.rawURL(selectedEntry.path) : null
  const activeMarkdown = !!selectedEntry && isMarkdown(selectedEntry.path)
  const activeLines = activeText === null ? null : activeText.split('\n').length - (activeText.endsWith('\n') ? 1 : 0)
  const editingCurrent = selectedEntry && edit?.path === selectedEntry.path ? edit : null

  const dirList = useFolderList(source, currentDir, selection.type === 'dir')

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
      expanded={effectiveExpanded}
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

  let statusSummary: string | null = null
  if (selectedEntry) {
    statusSummary = selectedEntry.type === 'symlink'
      ? t.folders.file.symlink(selectedEntry.symlinkTarget ?? '')
      : [selectedEntry.size !== null && t.folders.size(selectedEntry.size), t.folders.time(selectedEntry.modifiedAt), activeLines !== null && t.folders.file.lines(activeLines)].filter(Boolean).join(' · ')
  } else if (dirList.isSuccess) {
    const entries = dirList.data.pages.flatMap((page) => page.entries)
    const dirs = entries.filter((entry) => entry.type === 'dir').length
    const nonDirs = entries.length - dirs
    const bytes = entries.reduce((sum, entry) => sum + (entry.size ?? 0), 0)
    statusSummary = dirList.hasNextPage
      ? `${t.folders.partial(entries.length)} · ${t.folders.size(bytes)}+`
      : nonDirs > 0
        ? `${t.folders.summary(dirs, nonDirs)} · ${t.folders.size(bytes)}`
        : t.folders.summary(dirs, nonDirs)
  }

  const tooltipText = typeof note === 'string' && note ? `${note} ${t.folders.concurrent}` : t.folders.concurrent

  return (
    <div
      data-file-manager
      className={cn(
        '@container relative flex min-h-0 flex-col gap-0 overflow-hidden text-ink',
        variant === 'sheet'
          ? cn('min-w-0 flex-1', className)
          : variant === 'panel'
            ? cn('rounded-card border border-hairline-strong bg-surface-canvas', className)
            : cn('rounded-card border border-hairline bg-surface-control', className),
      )}
      onDragOver={(event) => { if (event.dataTransfer.types.includes('Files') || event.dataTransfer.types.includes(INTERNAL)) { event.preventDefault(); if (event.dataTransfer.types.includes('Files')) setDraggingFiles(true) } }}
      onDragLeave={(event) => { if (!(event.relatedTarget instanceof Node) || !event.currentTarget.contains(event.relatedTarget)) { setDraggingFiles(false); setDropTarget(null) } }}
      onDrop={(event) => onDropOn(dropTarget ?? currentDir, event)}
    >
      <input ref={fileInput} type="file" multiple className="sr-only" tabIndex={-1} aria-hidden="true" onChange={onPick} />
      <input ref={folderInput} type="file" multiple className="sr-only" tabIndex={-1} aria-hidden="true" onChange={onPick} {...{ webkitdirectory: '' }} />

      {/* Single unified 40px contextual toolbar: directory actions in directory mode, file actions in file viewing/editing mode. */}
      <div
        data-folder-toolbar
        className={cn(
          'flex h-10 shrink-0 items-center gap-2 border-b border-hairline px-3',
          sheet ? 'bg-surface-canvas' : 'bg-surface-subtle',
          editingCurrent && 'bg-tint-yellow/50',
        )}
      >
        <ToolButton className="@min-[40rem]:hidden" icon={<PanelLeftIcon />} label={treeOpen ? t.folders.hideTree : t.folders.showTree} showLabel={false} aria-expanded={treeOpen} onClick={() => setTreeOpen((value) => !value)} />
        <Breadcrumbs rootLabel={rootLabel} path={selection.path} entryType={selectedEntry?.type} onNavigate={(path) => go(path ? { path, type: 'dir', entry: null } : ROOT)} />
        <div className="ml-auto flex shrink-0 items-center gap-1">
          {selectedEntry ? (
            editingCurrent ? (
              <>
                <span className="mr-1 inline-flex items-center gap-1.5 text-[12px] font-medium whitespace-nowrap text-ink-700 @max-[32rem]:hidden">
                  {t.folders.file.editing}
                </span>
                {dirty && (
                  <span className="mr-1 inline-flex items-center gap-1 text-[12px] font-medium whitespace-nowrap text-pink-text">
                    <span aria-hidden="true" className="size-1.5 rounded-full bg-pink-text" />
                    {t.folders.file.unsaved}
                  </span>
                )}
                <Button size="xs" variant="ghost" className="h-7 no-underline" onClick={() => (dirty ? setDialog({ type: 'discard', next: null }) : setEdit(null))} disabled={editingCurrent.saving}>
                  {t.folders.file.cancel}
                </Button>
                <Button size="xs" className="h-7 px-3" onClick={() => void save()} disabled={editingCurrent.saving || !dirty} title={t.folders.file.saveKey}>
                  {editingCurrent.saving ? t.folders.file.saving : t.folders.file.save}
                </Button>
              </>
            ) : (
              <>
                {activeMarkdown && activeText !== null && (
                  <div role="radiogroup" aria-label={t.folders.file.view} className="mr-1 inline-flex h-7 items-center rounded-ticket border border-hairline-strong bg-surface-subtle p-0.5">
                    {(['preview', 'source'] as const).map((value) => (
                      <button
                        key={value}
                        type="button"
                        role="radio"
                        aria-checked={mode === value}
                        onClick={() => setMode(value)}
                        className={cn(
                          'h-[22px] cursor-pointer rounded-[4px] px-2 text-[11.5px] font-medium text-ink-700',
                          mode === value && 'bg-surface-control text-ink shadow-xs',
                        )}
                      >
                        {value === 'preview' ? t.folders.file.preview : t.folders.file.source}
                      </button>
                    ))}
                  </div>
                )}
                {activeOpenRawURL && (
                  <ToolLink
                    pill={sheet}
                    className="@max-[34rem]:px-1.5 @max-[34rem]:[&>span]:hidden"
                    icon={<ExternalLinkIcon />}
                    label={t.folders.openInNewTab}
                    href={activeOpenRawURL}
                  />
                )}
                {selectedEntry.type === 'file' && (
                  <ToolButton
                    pill={sheet}
                    icon={<PencilIcon />}
                    label={t.folders.file.edit}
                    onClick={() => activeFile.data?.kind === 'text' && startEdit(selectedEntry.path, activeFile.data.text, activeFile.data.format)}
                    disabled={!activeEditable}
                    title={activeEditable ? t.folders.file.edit : t.folders.file.notEditable}
                  />
                )}
                {selectedEntry.type === 'file' && (
                  <ToolButton icon={<DownloadIcon />} label={t.folders.download} showLabel={false} onClick={() => actions.download(selectedEntry)} />
                )}
                <EntryMenu entry={selectedEntry} actions={actions} className="size-7" />
              </>
            )
          ) : (
            <>
              <ToolButton pill={sheet} className="@max-[42rem]:px-1.5 @max-[42rem]:[&>span]:hidden" icon={<FilePlusIcon />} label={t.folders.newFile} onClick={() => actions.create(currentDir, 'file')} />
              <ToolButton pill={sheet} className="@max-[42rem]:px-1.5 @max-[42rem]:[&>span]:hidden" icon={<FolderPlusIcon />} label={t.folders.newFolder} onClick={() => actions.create(currentDir, 'dir')} />
              <UploadMenu pill={sheet} className="@max-[34rem]:px-1.5 @max-[34rem]:[&>span]:hidden" onFiles={() => actions.upload(currentDir)} onFolder={() => { uploadTarget.current = currentDir; folderInput.current?.click() }} />
              {source.git && <ToolButton pill={sheet} className="@max-[36rem]:px-1.5 @max-[36rem]:[&>span]:hidden" icon={<FolderGit2Icon />} label={t.folders.gitPull} onClick={() => setDialog({ type: 'git' })} />}
              <ToolButton icon={<RefreshCwIcon />} label={t.folders.refresh} showLabel={false} onClick={() => void client.invalidateQueries({ queryKey: folderKeys.all(source.key) })} />
            </>
          )}
        </div>
      </div>

      {/* Full-bleed 2-pane IDE split: 220px tree on the left, scrollable content pane on the right. */}
      <div className="relative flex min-h-0 flex-1">
        <aside
          aria-label={t.folders.tree}
          className={cn(
            'hidden w-[220px] shrink-0 overflow-y-auto border-r border-hairline @min-[40rem]:block',
            sheet ? 'bg-surface-subtle/30' : 'bg-surface-subtle',
          )}
        >
          {tree}
        </aside>
        {treeOpen && (
          <div className="absolute inset-0 z-20 flex @min-[40rem]:hidden">
            <aside aria-label={t.folders.tree} className="w-[min(18rem,85%)] overflow-y-auto border-r border-hairline-strong bg-surface-elevated shadow-floating">{tree}</aside>
            <button type="button" aria-label={t.folders.hideTree} className="flex-1 cursor-default bg-black/40" onClick={() => setTreeOpen(false)} />
          </div>
        )}
        <section data-folder-content className="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden" aria-label={t.folders.label}>
          {displayError != null && <ErrorAlert error={displayError} className="mx-3 mt-3 w-auto" />}
          {locatingPending ? (
            <Pending />
          ) : selection.type === 'dir' ? (
            <DirView
              key={selection.path}
              source={source}
              path={selection.path}
              actions={actions}
              onCreateEntry={() => void createEntryFile().catch(() => {})}
              onEditEntry={(entry) => {
                go({ path: entry.path, type: 'file', entry })
                const file = client.getQueryData<FileText>(folderKeys.file(source.key, entry.path))
                if (file?.kind === 'text') startEdit(entry.path, file.text, file.format)
              }}
            />
          ) : selectedEntry ? (
            <FileView
              source={source}
              entry={selectedEntry}
              limits={limits}
              actions={actions}
              file={activeFile}
              edit={edit}
              mode={mode}
              onDraft={(text) => setEdit((old) => (old ? { ...old, text } : old))}
              onSave={() => void save()}
            />
          ) : null}
        </section>
      </div>

      {pull?.status === 'failed' && <GitPullHint pull={pull} />}
      {pull?.status === 'failed' && pullDetails && pull.errorMessage && (
        <pre className="max-h-32 shrink-0 overflow-auto border-t border-hairline bg-tint-red/40 px-3 py-2 font-mono text-[11.5px] leading-[1.6] whitespace-pre-wrap text-red-text">{pull.errorMessage}</pre>
      )}
      <div
        data-folder-footer
        className="flex h-7 shrink-0 items-center justify-between gap-3 border-t border-hairline bg-surface-subtle/50 px-3 text-[11.5px] text-ink-500"
      >
        {/* Narrow screens can't fit both parts: when there is a pull status, show only the status. */}
        <span className={cn('flex min-w-0 items-center gap-1.5 tabular-nums', pull && '@max-[40rem]:hidden')}>
          {statusSummary && <span className="truncate">{statusSummary}</span>}
          <span title={tooltipText} className="inline-flex shrink-0 cursor-help items-center text-ink-500/80 hover:text-ink">
            <InfoIcon aria-hidden="true" className="size-3.5" />
            <span className="sr-only">{note && <>{note} </>}{t.folders.concurrent}</span>
          </span>
        </span>
        {pull && <PullStatus pull={pull} details={pullDetails} onDetails={() => setPullDetails((value) => !value)} onShow={() => go({ path: pull.path, type: 'dir', entry: null })} onRetry={() => setDialog({ type: 'git' })} onDismiss={() => { setDismissed((old) => new Set([...old, pull.id])); setPullDetails(false) }} />}
      </div>

      {uploads.rows.length > 0 && <UploadPanel rows={uploads.rows} onClose={uploads.clear} />}
      {draggingFiles && (
        <div aria-hidden="true" className="pointer-events-none absolute inset-1.5 z-30 flex items-center justify-center rounded-[12px] border border-dashed border-hairline-strong bg-surface-hover/85">
          <span className="rounded-full bg-surface-elevated px-4 py-2 text-[13.5px] font-semibold text-ink shadow-floating">{t.folders.drop((dropTarget ?? currentDir) || rootLabel)}</span>
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
}

// A Git pull in the status bar: in progress, succeeded (you can go and look), or failed (you can view the git output and retry).
function PullStatus({ pull, details, onDetails, onShow, onRetry, onDismiss }: { pull: GitPull; details: boolean; onDetails: () => void; onShow: () => void; onRetry: () => void; onDismiss: () => void }) {
  const ended = pull.status === 'succeeded' || pull.status === 'failed'
  return (
    <span role="status" className={cn('ml-auto flex min-w-0 shrink-0 items-center gap-1.5 font-medium', pull.status === 'failed' ? 'text-red-text' : pull.status === 'succeeded' ? 'text-green-text' : 'text-ink-700')}>
      {pull.status === 'failed' ? <CircleAlertIcon aria-hidden="true" className="size-3.5" /> : pull.status === 'succeeded' ? <CircleCheckIcon aria-hidden="true" className="size-3.5" /> : <LoaderCircleIcon aria-hidden="true" className="riso-spin size-3.5 motion-reduce:animate-none" />}
      <span className="max-w-[24rem] truncate" title={pull.url}>
        {pull.status === 'queued' ? t.folders.git.queued(pull.url, pull.path) : pull.status === 'running' ? t.folders.git.running(pull.url, pull.path) : pull.status === 'succeeded' ? t.folders.git.succeeded(pull.path) : t.folders.git.failed(pull.errorCode ? t.folders.git.errors[pull.errorCode] : t.folders.git.errors.clone_failed)}
      </span>
      {pull.status === 'succeeded' && <button type="button" className="cursor-pointer text-riso-blue underline" onClick={onShow}>{t.folders.git.show}</button>}
      {pull.status === 'failed' && pull.errorMessage && <button type="button" aria-expanded={details} className="cursor-pointer underline" onClick={onDetails}>{t.folders.git.details}</button>}
      {pull.status === 'failed' && <button type="button" className="cursor-pointer text-riso-blue underline" onClick={onRetry}>{t.folders.git.retry}</button>}
      {ended && <button type="button" aria-label={t.folders.git.dismiss} className="cursor-pointer rounded-full p-0.5 hover:bg-surface-hover" onClick={onDismiss}><XIcon aria-hidden="true" className="size-3.5" /></button>}
    </span>
  )
}

// Path bar: single-line breadcrumb with plain EntryIcon; the root and every folder level can be clicked to go back to that level.
function Breadcrumbs({ rootLabel, path, entryType, onNavigate, className }: { rootLabel: string; path: string; entryType?: FolderEntry['type']; onNavigate: (path: string) => void; className?: string }) {
  const parts = segmentsOf(path)
  return (
    <nav aria-label={t.folders.path} className={cn('flex min-w-0 flex-1 items-center gap-0.5 overflow-hidden text-[12.5px]', className)}>
      <button type="button" onClick={() => onNavigate('')} className={cn('inline-flex min-w-0 shrink cursor-pointer items-center gap-1.5 rounded-[6px] px-1.5 py-0.5 hover:bg-surface-hover', parts.length === 0 ? 'font-semibold' : 'font-medium text-ink-700')} aria-current={parts.length === 0 ? 'page' : undefined}>
        <EntryIcon path="" type="dir" open={parts.length === 0} />
        <span className="truncate">{rootLabel}</span>
      </button>
      {parts.map((part, index) => {
        const last = index === parts.length - 1, target = parts.slice(0, index + 1).join('/')
        return (
          <span key={target} className={cn('flex min-w-0 items-center gap-0.5', !last && 'shrink-[2]')}>
            <span aria-hidden="true" className="text-ink-500/60">/</span>
            {last ? (
              <span aria-current="page" className="inline-flex min-w-0 items-center gap-1.5 px-1 font-semibold">
                {entryType && entryType !== 'dir' && <EntryIcon path={path} type={entryType} className="size-3.5" />}
                <span className="truncate">{part}</span>
                {entryType && entryType !== 'dir' && isEntryFile(path) && <EntryBadge />}
              </span>
            ) : (
              <button type="button" onClick={() => onNavigate(target)} className="cursor-pointer truncate rounded-[6px] px-1.5 py-0.5 font-medium text-ink-700 hover:bg-surface-hover">{part}</button>
            )}
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
    <section aria-label={t.folders.upload} className="absolute right-3 bottom-9 z-20 w-[min(20rem,calc(100%-1.5rem))] rounded-card border border-hairline-strong bg-surface-elevated shadow-floating">
      <header className="flex items-center gap-2 px-3.5 pt-2.5 pb-2">
        <h3 role="status" className="text-[13px] font-semibold">{t.folders.uploads.title(done, rows.length, finished)}</h3>
        {failed > 0 && <span className="text-[12px] font-medium text-red-text">{t.folders.uploads.failed(failed)}</span>}
        {finished && <button type="button" aria-label={t.folders.uploads.close} onClick={onClose} className="ml-auto cursor-pointer rounded-full p-1 text-ink-500 hover:bg-surface-hover"><XIcon aria-hidden="true" className="size-3.5" /></button>}
      </header>
      <div className="mx-3.5 h-1.5 overflow-hidden rounded-full bg-surface-active"><div className={cn('h-full rounded-full', failed ? 'bg-red' : 'bg-ink')} style={{ width: `${total ? Math.round((loaded / total) * 100) : 100}%` }} /></div>
      <ul className="mt-1.5 max-h-36 overflow-y-auto px-1.5 pb-1.5">
        {rows.map((row) => (
          <li key={row.id} className="flex h-7 items-center gap-2 rounded-[6px] px-2 text-[12.5px]" title={row.error ?? row.path}>
            <span className="min-w-0 flex-1 truncate">{row.path}</span>
            <span className={cn('shrink-0 tabular-nums', row.state === 'failed' ? 'font-medium text-red-text' : row.state === 'done' ? 'text-green-text' : 'text-ink-500')}>
              {row.state === 'done' ? <CircleCheckIcon aria-hidden="true" className="size-3.5" /> : row.state === 'failed' ? <CircleAlertIcon aria-hidden="true" className="size-3.5" /> : `${row.size ? Math.round((row.loaded / row.size) * 100) : 0}%`}
            </span>
          </li>
        ))}
      </ul>
    </section>
  )
}
