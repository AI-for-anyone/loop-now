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

// 与契约的 maxItems 相同。
const MAX_DIRECTORIES = 50
const copy = t.localMode.directories
const linkClass = 'font-black underline decoration-riso-pink decoration-2 underline-offset-4'

const toInput = (entry: Directory): DirectoryInput => ({ path: entry.path, note: entry.note })
const isConflict = (error: unknown) => error instanceof ApiError && error.status === 412

// 清单整体保存：把一次增、改、删套在最新的清单上，得到整份新清单与改动那一行的下标（用来对上服务端的字段错误）。
// 要改或删的一项已不在清单里时为 null。
function applyChange(listing: Listing, target: Target, value: DirectoryInput | null) {
  const current = listing.directories.map(toInput)
  if (target.kind === 'add') return value ? { directories: [...current, value], index: current.length } : null
  const index = listing.directories.findIndex((entry) => entry.path === target.entry.path)
  if (index < 0) return null
  return { directories: value ? current.map((entry, i) => (i === index ? value : entry)) : current.filter((_, i) => i !== index), index }
}

// 项目设置的“本机目录”卡片内容（卡片标题由项目设置页给出）。
export function ProjectLocalDirectories({ projectId }: { projectId: string }) {
  const query = useProjectLocalDirectories(projectId)
  const put = usePutProjectLocalDirectories(projectId)
  return (
    <>
      <p className="-mt-1 text-[14px] text-ink-700">{copy.intro.project}</p>
      <Cautions />
      <DirectoriesOffNote />
      <Loaded query={query}>
        {(data) => <DirectoryEditor listing={data} save={put.mutateAsync} reload={async () => (await query.refetch()).data} />}
      </Loaded>
    </>
  )
}

// Agent 配置的“本机目录”区块：项目 Agent 先列出继承自项目的条目（只读），再是它自己的；平台 Agent 只有自己的。
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

// 页面上如实写明的两点：改动直接落在真实文件上；清单不是访问隔离。
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

// “绑定本机目录”关着时，清单照常可改，但要说明它现在不交给 Agent。读不到开关时不提示。
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

// 一行：路径、备注；目录不存在、不是目录或读不了时在行内标出，这一项暂不交给 Agent，但不妨碍保存。
function DirectoryRow({ entry, actions }: { entry: Directory; actions?: ReactNode }) {
  const problem = entry.status === 'ok' ? null : entry.status
  return (
    <li aria-label={entry.path} className="flex flex-wrap items-start gap-x-3 gap-y-2 rounded-card bg-white px-4 py-3">
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
      {actions && <div className="ml-auto flex shrink-0 gap-1">{actions}</div>}
    </li>
  )
}

function DirectoryEditor({ listing, save, reload }: { listing: Listing; save: Save; reload: Reload }) {
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
      // 移除的意图不因别处的修改而变：读取最新清单后再移除一次；那一项已经不在就算完成。
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
        onConfirm={() => (removing ? remove(removing) : undefined)}
      />
    </>
  )
}

// 添加或编辑一项。保存的是整份清单：服务端的字段错误按改动那一行的下标对到输入框下；别处改过清单（412）时读取最新内容，
// 输入保留，再保存时套在新清单上。
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
      <DialogContent className="sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>{target.kind === 'add' ? copy.dialog.addTitle : copy.dialog.editTitle}</DialogTitle>
          <DialogDescription>{copy.cautions[0]}</DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(event) => void submit(event)} noValidate>
          <div className="grid gap-1.5">
            <Label htmlFor={ids.path}>{copy.dialog.path}</Label>
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
