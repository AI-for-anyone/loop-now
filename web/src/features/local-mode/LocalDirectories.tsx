import { useId, useState, type FormEvent, type ReactNode } from 'react'
import { Link } from 'react-router'
import type { UseQueryResult } from '@tanstack/react-query'
import { PlusIcon, TriangleAlertIcon } from 'lucide-react'
import { ApiError, type Schemas } from '@/api/client'
import { useAgentLocalDirectories, useLocalModeSettings, useProjectLocalDirectories, usePutAgentLocalDirectories, usePutProjectLocalDirectories } from '@/api/local-mode'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Section } from '@/features/agent-config/Section'
import { PathPicker } from '@/features/desktop/PathPicker'
import { useScope } from '@/features/projects/scope-context'
import { fieldMessages, unmatchedFieldError } from '@/lib/errors'
import { projectSettingsPath } from '@/lib/scope'
import { t } from '@/i18n'
import { localModeSettingsPath } from './LocalModeCard'

type Directory = Schemas['LocalDirectory']
type DirectoryInput = Schemas['LocalDirectoryInput']
type Listing = { revision: number; directories: Directory[] }
type Target = { kind: 'add' } | { kind: 'edit'; entry: Directory }
type Save = (body: Schemas['LocalDirectoriesPut']) => Promise<unknown>
type Reload = () => Promise<Listing | undefined>

// Same as maxItems in the contract.
const MAX_DIRECTORIES = 50
const copy = t.localMode.directories
const linkClass = 'font-black underline decoration-riso-pink decoration-2 underline-offset-4'

const toInput = (entry: Directory): DirectoryInput => ({ path: entry.path, note: entry.note })
const isConflict = (error: unknown) => error instanceof ApiError && error.status === 412

// The list is saved as a whole: apply one add, edit or delete to the latest list to get the complete new list plus the index of
// the changed row (used to match the server's field errors).
// null when the entry to edit or delete is no longer in the list.
function applyChange(listing: Listing, target: Target, value: DirectoryInput | null) {
  const current = listing.directories.map(toInput)
  if (target.kind === 'add') return value ? { directories: [...current, value], index: current.length } : null
  const index = listing.directories.findIndex((entry) => entry.path === target.entry.path)
  if (index < 0) return null
  return { directories: value ? current.map((entry, i) => (i === index ? value : entry)) : current.filter((_, i) => i !== index), index }
}

// The content of the “本机目录” (Local directories) card in project settings (the project settings page provides the card title).
export function ProjectLocalDirectories({ projectId }: { projectId: string }) {
  const query = useProjectLocalDirectories(projectId)
  const put = usePutProjectLocalDirectories(projectId)
  return (
    <>
      <p className="-mt-1 text-[14px] text-ink-700">{copy.intro.project}</p>
      <Cautions />
      <DirectoriesOffNote />
      <Loaded query={query}>
        {(data) => (
          <DirectoryEditor
            listing={data}
            save={put.mutateAsync}
            reload={async () => (await query.refetch()).data}
            // A project's folder can bring its terminal conversations in: the import page, for that folder and into this project.
            extra={(entry) => (
              <Button size="sm" variant="ghost" asChild>
                <Link to={`/import?${new URLSearchParams({ path: entry.path, project: projectId })}`} aria-label={t.importing.entry.projectLabel(entry.path)}>
                  {t.importing.entry.project}
                </Link>
              </Button>
            )}
          />
        )}
      </Loaded>
    </>
  )
}

// The “本机目录” (Local directories) block in Agent config: a project Agent first lists the entries inherited from the project
// (read-only), then its own; a platform Agent only has its own.
export function AgentLocalDirectoriesSection({ agent }: { agent: Schemas['Agent'] }) {
  const scope = useScope()
  const query = useAgentLocalDirectories(agent.id)
  const put = usePutAgentLocalDirectories(agent.id)
  const project = agent.scope === 'project' && scope.kind === 'project' ? scope.project : null
  return (
    <Section id="local-dirs" title={copy.title}>
      <p className="text-[13.5px] leading-[1.7] text-ink-700">{project ? copy.intro.agent : copy.intro.platform}</p>
      <Cautions />
      <DirectoriesOffNote />
      <Loaded query={query}>
        {(data) => (
          <>
            {project && data.projectId === project.id && <InheritedDirectories projectName={project.name} entries={data.inherited} />}
            {project && <h3 className="text-[15px] font-black">{copy.own}</h3>}
            <DirectoryEditor listing={data} save={put.mutateAsync} reload={async () => (await query.refetch()).data} />
          </>
        )}
      </Loaded>
    </Section>
  )
}

// Two points the page states plainly: changes land directly on the real files, and the list does not isolate access.
function Cautions() {
  return (
    <Alert variant="warning" role="note">
      <TriangleAlertIcon />
      <AlertTitle>{copy.cautionTitle}</AlertTitle>
      <AlertDescription className="text-ink">
        <ul className="list-disc pl-5 text-[13.5px] leading-[1.7]">
          {copy.cautions.map((caution) => (
            <li key={caution}>{caution}</li>
          ))}
        </ul>
      </AlertDescription>
    </Alert>
  )
}

// When “绑定本机目录” (Bind local directories) is off, the list can still be edited as usual, but say that Agents aren't told
// about it right now. No note when the switch can't be read.
function DirectoriesOffNote() {
  const settings = useLocalModeSettings()
  if (settings.data?.directories !== false) return null
  return (
    <Alert variant="info" role="note">
      <AlertDescription className="text-[13.5px] text-ink">
        {copy.off}{' '}
        <Link to={localModeSettingsPath} className={linkClass}>
          {copy.openSettings}
        </Link>
      </AlertDescription>
    </Alert>
  )
}

function Loaded<T extends Listing>({ query, children }: { query: UseQueryResult<T, Error>; children: (data: T) => ReactNode }) {
  if (query.data) return children(query.data)
  if (query.isError)
    return (
      <div className="grid gap-2">
        <ErrorAlert error={query.error} />
        <Button size="sm" variant="secondary" className="justify-self-start" onClick={() => void query.refetch()}>
          {t.common.retry}
        </Button>
      </div>
    )
  return <p role="status" className="text-[13.5px] text-ink-500">{t.common.loading}</p>
}

function InheritedDirectories({ projectName, entries }: { projectName: string; entries: Directory[] }) {
  const title = copy.inherited.title(projectName)
  return (
    <div role="region" aria-label={title} className="grid min-w-0 gap-2.5 rounded-card bg-tint-blue p-4">
      <h3 className="text-[15px] font-black [overflow-wrap:anywhere]">{title}</h3>
      <p className="-mt-1 text-[12.5px] text-ink-700">{copy.inherited.hint}</p>
      {entries.length > 0 ? <DirectoryRows label={title} entries={entries} /> : <p className="text-[13.5px] text-ink-500">{copy.inherited.empty}</p>}
      <Link to={projectSettingsPath({ name: projectName })} className={`justify-self-start text-[13.5px] ${linkClass}`}>
        {copy.inherited.edit}
      </Link>
    </div>
  )
}

function DirectoryRows({ label, entries, actions }: { label: string; entries: Directory[]; actions?: (entry: Directory) => ReactNode }) {
  return (
    <ul aria-label={label} className="grid gap-2">
      {entries.map((entry, index) => (
        <DirectoryRow key={`${index}:${entry.path}`} entry={entry} actions={actions?.(entry)} />
      ))}
    </ul>
  )
}

// One row: path and note; when the directory doesn't exist, isn't a directory or can't be read, this is marked in the row and
// Agents aren't told about this entry for now, but saving is not blocked.
function DirectoryRow({ entry, actions }: { entry: Directory; actions?: ReactNode }) {
  const problem = entry.status === 'ok' ? null : entry.status
  return (
    <li aria-label={entry.path} className="flex flex-wrap items-start gap-x-3 gap-y-2 rounded-card border border-hairline bg-surface-control px-4 py-3 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-white">
      <div className="grid min-w-0 flex-[1_1_260px] gap-1">
        <span className="font-mono text-[13.5px] font-bold break-all">{entry.path}</span>
        {entry.note && <span className="text-[13.5px] text-ink-700 [overflow-wrap:anywhere]">{entry.note}</span>}
        {problem && (
          <span className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[12.5px] text-ink-700">
            <Badge tone="warning">
              <TriangleAlertIcon aria-hidden="true" />
              {copy.status[problem]}
            </Badge>
            {copy.statusHint}
          </span>
        )}
      </div>
      {actions && <div className="ml-auto flex max-w-full flex-wrap justify-end gap-1">{actions}</div>}
    </li>
  )
}

function DirectoryEditor({ listing, save, reload, extra }: { listing: Listing; save: Save; reload: Reload; extra?: (entry: Directory) => ReactNode }) {
  const [target, setTarget] = useState<Target | null>(null)
  const [removing, setRemoving] = useState<Directory | null>(null)
  const [removeOpen, setRemoveOpen] = useState(false)
  const full = listing.directories.length >= MAX_DIRECTORIES

  const remove = async (entry: Directory) => {
    const attempt = async (from: Listing) => {
      const change = applyChange(from, { kind: 'edit', entry }, null)
      if (change) await save({ expectedRevision: from.revision, directories: change.directories })
    }
    try {
      await attempt(listing)
    } catch (error) {
      if (!isConflict(error)) throw error
      // The intent to remove doesn't change because of edits elsewhere: load the latest list and remove again; if the entry is
      // already gone, that counts as done.
      const latest = await reload()
      if (latest) await attempt(latest)
    }
  }

  return (
    <>
      {listing.directories.length === 0 ? (
        <p className="text-[13.5px] text-ink-500">{copy.empty}</p>
      ) : (
        <DirectoryRows
          label={copy.list}
          entries={listing.directories}
          actions={(entry) => (
            <>
              {extra?.(entry)}
              <Button size="sm" variant="ghost" aria-label={copy.editLabel(entry.path)} onClick={() => setTarget({ kind: 'edit', entry })}>
                {copy.edit}
              </Button>
              <Button
                size="sm"
                variant="ghost"
                aria-label={copy.removeLabel(entry.path)}
                onClick={() => {
                  setRemoving(entry)
                  setRemoveOpen(true)
                }}
              >
                {copy.remove}
              </Button>
            </>
          )}
        />
      )}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <Button variant="secondary" disabled={full} onClick={() => setTarget({ kind: 'add' })}>
          <PlusIcon />
          {copy.add}
        </Button>
        {full && <span className="text-[13px] text-ink-500">{copy.full(MAX_DIRECTORIES)}</span>}
      </div>
      {target && <DirectoryDialog target={target} listing={listing} save={save} reload={reload} onClose={() => setTarget(null)} />}
      <ConfirmDialog
        open={removeOpen}
        onOpenChange={setRemoveOpen}
        title={removing ? copy.removeTitle(removing.path) : ''}
        body={copy.removeBody}
        confirmLabel={copy.remove}
        destructive
        // This only removes it from the list; the directory and its files are still there and it can be added back.
        irreversible={false}
        onConfirm={() => (removing ? remove(removing) : undefined)}
      />
    </>
  )
}

// Add or edit an entry. The whole list is saved: the server's field errors are matched to the inputs by the index of the
// changed row; when the list was changed elsewhere (412), load the latest content, keep the input, and apply it to the new
// list on the next save.
function DirectoryDialog({ target, listing, save, reload, onClose }: { target: Target; listing: Listing; save: Save; reload: Reload; onClose: () => void }) {
  const initial = target.kind === 'edit' ? target.entry : null
  const [path, setPath] = useState(initial?.path ?? '')
  const [note, setNote] = useState(initial?.note ?? '')
  const [error, setError] = useState<unknown>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [index, setIndex] = useState<number | null>(null)
  const [busy, setBusy] = useState(false)
  const ids = { path: useId(), pathHint: useId(), note: useId(), noteHint: useId() }
  const value: DirectoryInput = { path: path.trim(), note: note.trim() || null }
  const unchanged = initial !== null && value.path === initial.path && value.note === initial.note

  const fieldName = (name: 'path' | 'note') => `directories[${index}].${name}`
  const pathError = index === null ? undefined : fieldMessages(error, (field) => field === fieldName('path'))
  const noteError = index === null ? undefined : fieldMessages(error, (field) => field === fieldName('note'))
  const otherError = index === null ? error : unmatchedFieldError(error, (field) => field === fieldName('path') || field === fieldName('note'))

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!value.path || unchanged || busy) return
    setError(null)
    setNotice(null)
    const change = applyChange(listing, target, value)
    if (!change) {
      setNotice(copy.gone)
      return
    }
    setIndex(change.index)
    setBusy(true)
    try {
      await save({ expectedRevision: listing.revision, directories: change.directories })
      onClose()
    } catch (caught) {
      if (isConflict(caught)) {
        await reload().catch(() => undefined)
        setNotice(copy.conflict)
      } else setError(caught)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !busy && onClose()}>
      <DialogContent kind={target.kind === 'add' ? 'create' : 'edit'} className="sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>{target.kind === 'add' ? copy.dialog.addTitle : copy.dialog.editTitle}</DialogTitle>
          <DialogDescription>{copy.cautions[0]}</DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(event) => void submit(event)} noValidate>
          <div className="grid gap-1.5">
            <Label htmlFor={ids.path}>{copy.dialog.path}</Label>
            <PathPicker kind="directory" value={path} onPick={setPath} disabled={busy}>
              <Input
                id={ids.path}
                value={path}
                onChange={(event) => setPath(event.target.value)}
                aria-describedby={ids.pathHint}
                aria-invalid={pathError ? true : undefined}
                autoFocus
                spellCheck={false}
                autoComplete="off"
                className="font-mono text-[13px]"
              />
            </PathPicker>
            <FieldError message={pathError} />
            <p id={ids.pathHint} className="text-[12.5px] leading-snug text-ink-500">
              {copy.dialog.pathHint}
            </p>
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor={ids.note}>{copy.dialog.note}</Label>
            <Input
              id={ids.note}
              value={note}
              onChange={(event) => setNote(event.target.value)}
              aria-describedby={ids.noteHint}
              aria-invalid={noteError ? true : undefined}
              autoComplete="off"
            />
            <FieldError message={noteError} />
            <p id={ids.noteHint} className="text-[12.5px] leading-snug text-ink-500">
              {copy.dialog.noteHint}
            </p>
          </div>
          {notice && (
            <Alert variant="warning" role="status">
              <AlertDescription className="text-[13.5px] text-ink">{notice}</AlertDescription>
            </Alert>
          )}
          <ErrorAlert error={otherError} />
          <DialogFooter>
            <Button type="button" variant="secondary" disabled={busy} onClick={onClose}>
              {t.common.cancel}
            </Button>
            <Button type="submit" disabled={!value.path || unchanged || busy}>
              {t.common.save}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
