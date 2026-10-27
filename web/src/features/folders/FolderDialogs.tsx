import { useId, useState, type FormEvent, type ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { t } from '@/i18n'
import { folderPath, nameOf, parentOf, type FolderEntry, type GitPullInput } from './folder-source'

function Field({ label, hint, children, id }: { label: string; hint?: string; children: (describedBy: string | undefined) => ReactNode; id: string }) {
  const hintId = useId()
  return (
    <div className="grid gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children(hint ? hintId : undefined)}
      {hint && <p id={hintId} className="text-[12.5px] leading-snug text-ink-500">{hint}</p>}
    </div>
  )
}

// 仓库地址的末段作为默认的目标文件夹：https://github.com/acme/budget-ledger.git → budget-ledger。
const repoName = (url: string) => url.trim().replace(/\.git$/, '').replace(/\/+$/, '').split(/[/:]/).at(-1) ?? ''
// https 地址里带用户名或密码（user:token@host）时拒绝；ssh 形式的 git@host:path 不在此列。
const hasCredentials = (url: string) => /^[a-z][a-z0-9+.-]*:\/\/[^/@]+@/i.test(url.trim())

export function GitPullDialog({ onClose, onSubmit }: { onClose: () => void; onSubmit: (input: GitPullInput) => Promise<void> }) {
  const [url, setUrl] = useState(''), [branch, setBranch] = useState('main'), [target, setTarget] = useState(''), [targetTouched, setTargetTouched] = useState(false)
  const [error, setError] = useState<unknown>(null), [busy, setBusy] = useState(false)
  const ids = { url: useId(), branch: useId(), target: useId() }
  const path = folderPath(targetTouched ? target : target || repoName(url))
  const valid = url.trim() !== '' && !hasCredentials(url) && branch.trim() !== '' && path !== null
  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!valid || busy) return
    setBusy(true); setError(null)
    try { await onSubmit({ url: url.trim(), branch: branch.trim(), path: path! }); onClose() } catch (caught) { setError(caught) } finally { setBusy(false) }
  }
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-[520px]">
        <DialogHeader>
          <DialogTitle>{t.folders.git.title}</DialogTitle>
          <DialogDescription>{t.folders.git.intro}</DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(event) => void submit(event)} noValidate>
          <Field id={ids.url} label={t.folders.git.url} hint={t.folders.git.urlHint}>
            {(describedBy) => <Input id={ids.url} value={url} onChange={(event) => setUrl(event.target.value)} placeholder="https://github.com/acme/budget-ledger.git" aria-describedby={describedBy} aria-invalid={hasCredentials(url)} autoFocus spellCheck={false} className="font-mono text-[13px]" />}
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field id={ids.branch} label={t.folders.git.branch} hint={t.folders.git.branchHint}>
              {(describedBy) => <Input id={ids.branch} value={branch} onChange={(event) => setBranch(event.target.value)} aria-describedby={describedBy} spellCheck={false} className="font-mono text-[13px]" />}
            </Field>
            <Field id={ids.target} label={t.folders.git.target} hint={t.folders.git.targetHint}>
              {(describedBy) => <Input id={ids.target} value={targetTouched ? target : target || repoName(url)} onChange={(event) => { setTarget(event.target.value); setTargetTouched(true) }} aria-describedby={describedBy} aria-invalid={path === null} spellCheck={false} className="font-mono text-[13px]" />}
            </Field>
          </div>
          <ErrorAlert error={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={onClose}>{t.common.cancel}</Button>
            <Button type="submit" disabled={!valid || busy}>{t.folders.git.submit}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function MoveDialog({ entry, onClose, onSubmit }: { entry: FolderEntry; onClose: () => void; onSubmit: (dir: string) => Promise<void> }) {
  const [value, setValue] = useState(parentOf(entry.path))
  const [error, setError] = useState<unknown>(null), [busy, setBusy] = useState(false)
  const id = useId()
  const dir = folderPath(value)
  const unchanged = dir === parentOf(entry.path)
  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (dir === null || unchanged || busy) return
    setBusy(true); setError(null)
    try { await onSubmit(dir); onClose() } catch (caught) { setError(caught) } finally { setBusy(false) }
  }
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-[480px]">
        <DialogHeader>
          <DialogTitle>{t.folders.moveDialog.title(nameOf(entry.path))}</DialogTitle>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(event) => void submit(event)} noValidate>
          <Field id={id} label={t.folders.moveDialog.label} hint={t.folders.moveDialog.hint}>
            {(describedBy) => <Input id={id} value={value} onChange={(event) => setValue(event.target.value)} aria-describedby={describedBy} aria-invalid={dir === null} autoFocus spellCheck={false} className="font-mono text-[13px]" />}
          </Field>
          <ErrorAlert error={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={onClose}>{t.common.cancel}</Button>
            <Button type="submit" disabled={dir === null || unchanged || busy}>{t.folders.moveDialog.confirm}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
