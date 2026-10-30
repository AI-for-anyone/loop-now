import { useId, useState, type FormEvent } from 'react'
import { TriangleAlertIcon } from 'lucide-react'
import { ApiError, type Schemas } from '@/api/client'
import { useBuiltinPluginSources, usePluginInstall } from '@/api/plugins'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { t } from '@/i18n'
import { localizedText } from '@/lib/localized-text'
import { cn } from '@/lib/utils'
import { ManifestSummary } from './ManifestSummary'
import { usePluginInstaller } from './usePluginInstaller'

type Preview = Schemas['PluginPreview']
type SourceKind = 'builtin' | 'git' | 'local'
const text = t.plugins.installEntry
const confirmText = t.plugins.confirm

const sourceLine = (source: Schemas['PluginResolvedSource']) =>
  [source.location, source.ref ?? source.npmVersion].filter(Boolean).join(' @ ') + (source.commit ? ` (${source.commit.slice(0, 7)})` : '')

// 安装与更新的第二步：展示预览里的清单，人确认后按预览安装。非官方来源要明确确认（confirm），
// 服务端仍认为需要确认（409 plugin_confirmation_required）时补上警告，再点一次即带 confirm 安装。
export function ConfirmInstallDialog({ preview, onClose, onInstalled }: { preview: Preview; onClose: () => void; onInstalled?: (plugin: Schemas['Plugin']) => void }) {
  const install = usePluginInstall()
  const [forced, setForced] = useState(false)
  const untrusted = !preview.trusted || forced
  const update = preview.installedVersion !== null
  const same = update && preview.installedVersion === preview.manifest.version
  const { manifest } = preview
  const run = () =>
    install.mutate(
      { preview, confirm: untrusted },
      {
        onSuccess: (plugin) => {
          onInstalled?.(plugin)
          onClose()
        },
        onError: (error) => {
          if (error instanceof ApiError && error.code === 'plugin_confirmation_required') setForced(true)
        },
      },
    )
  const label = install.isPending
    ? confirmText.installing
    : update
      ? untrusted
        ? confirmText.untrustedUpdate
        : confirmText.trustedUpdate
      : untrusted
        ? confirmText.untrustedInstall
        : confirmText.trustedInstall
  return (
    <Dialog open onOpenChange={(open) => !open && !install.isPending && onClose()}>
      <DialogContent className="sm:max-w-[700px]">
        <DialogHeader>
          <DialogTitle className="flex flex-wrap items-center gap-x-2.5 gap-y-1 pr-12">
            <span>{update ? confirmText.update(localizedText(manifest.displayName)) : confirmText.install(localizedText(manifest.displayName))}</span>
            <code className="rounded-[6px] bg-ink/[0.06] px-2 py-0.5 font-mono text-[12.5px] font-bold">
              {update && !same ? `${manifest.id} · ${confirmText.versionChange(preview.installedVersion!, manifest.version)}` : t.plugins.idVersion(manifest.id, manifest.version)}
            </code>
          </DialogTitle>
          <DialogDescription>{localizedText(manifest.description)}</DialogDescription>
        </DialogHeader>
        {untrusted && (
          <div role="note" className="flex gap-3 rounded-card bg-tint-yellow px-4 py-3 text-[13.5px] leading-normal">
            <TriangleAlertIcon aria-hidden="true" className="mt-0.5 size-5 shrink-0 text-ink" />
            <div className="min-w-0 [overflow-wrap:anywhere]">
              <p className="font-black">{confirmText.unverifiedTitle(t.plugins.from[preview.source.kind])}</p>
              <p>
                {sourceLine(preview.source)} · {confirmText.unverifiedBody}
              </p>
            </div>
          </div>
        )}
        {same ? <p role="status" className="text-[14px] font-bold">{confirmText.sameVersion(manifest.version)}</p> : <ManifestSummary manifest={manifest} />}
        <p className="text-[12.5px] text-ink-700">
          {!update && `${confirmText.notEnabled} · `}
          {confirmText.expires(t.chat.time(preview.expiresAt))}
        </p>
        <ErrorAlert error={install.error} />
        <DialogFooter>
          <Button variant="secondary" disabled={install.isPending} onClick={onClose}>
            {confirmText.cancel}
          </Button>
          {!same && (
            <Button disabled={install.isPending} onClick={run}>
              {label}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

const isAbsolutePath = (value: string) => value.startsWith('/') || /^[A-Za-z]:[\\/]/.test(value)

// 安装入口：三种来源（随平台发布的官方插件、git 仓库、服务端本机目录），读取清单后进入确认。
export function InstallSourceDialog({
  open,
  onOpenChange,
  installed,
  onInstalled,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  installed: string[]
  onInstalled?: (plugin: Schemas['Plugin']) => void
}) {
  const installer = usePluginInstaller(onInstalled)
  const builtin = useBuiltinPluginSources()
  const id = useId()
  const [kind, setKind] = useState<SourceKind>('builtin')
  const [location, setLocation] = useState('')
  const [ref, setRef] = useState('')
  const [builtinId, setBuiltinId] = useState('')
  const [invalid, setInvalid] = useState<string | null>(null)
  const sources = builtin.data?.sources ?? []
  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const target = kind === 'builtin' ? builtinId : location.trim()
    const problem = !target ? text.required : kind === 'local' && !isAbsolutePath(target) ? text.absolute : null
    setInvalid(problem)
    if (problem) return
    const source: Schemas['PluginSource'] = kind === 'git' && ref.trim() ? { kind, location: target, ref: ref.trim() } : { kind, location: target }
    if (await installer.begin('source', { source })) onOpenChange(false)
  }
  const error = installer.errorFor('source')
  return (
    <>
      <Dialog open={open} onOpenChange={(next) => installer.pending === null && onOpenChange(next)}>
        <DialogContent className="sm:max-w-[560px]">
          <DialogHeader>
            <DialogTitle>{text.title}</DialogTitle>
          </DialogHeader>
          <form className="grid min-w-0 gap-4" onSubmit={(event) => void submit(event)} noValidate>
            <fieldset className="grid gap-2">
              <legend className="mb-2 text-[13.5px] font-black">{text.source}</legend>
              <div role="radiogroup" className="flex flex-wrap gap-2">
                {(['builtin', 'git', 'local'] as const).map((value) => (
                  <label
                    key={value}
                    className={cn(
                      'inline-flex min-h-9 cursor-pointer items-center gap-2 rounded-full border-2 px-3.5 text-[13.5px] font-bold has-[:focus-visible]:shadow-focus',
                      kind === value ? 'border-ink bg-riso-yellow' : 'border-ink/20 bg-white',
                    )}
                  >
                    <input
                      type="radio"
                      name={`${id}-kind`}
                      value={value}
                      checked={kind === value}
                      onChange={() => {
                        setKind(value)
                        setInvalid(null)
                      }}
                      className="sr-only"
                    />
                    {text.sources[value]}
                  </label>
                ))}
              </div>
            </fieldset>
            {kind === 'builtin' ? (
              <fieldset className="grid gap-2">
                <legend className="mb-1 text-[13.5px] font-black">{text.builtin}</legend>
                <ErrorAlert error={builtin.error} />
                {builtin.isPending && <p role="status">{t.plugins.loading}</p>}
                {builtin.isSuccess && sources.length === 0 && <p className="text-[13.5px] text-ink-700">{t.plugins.builtinEmpty}</p>}
                <ul className="grid gap-1.5">
                  {sources.map((source) => {
                    const done = installed.includes(source.id)
                    return (
                      <li key={source.id}>
                        <label className={cn('flex min-w-0 cursor-pointer items-start gap-2.5 rounded-input bg-white px-3 py-2.5 shadow-[inset_0_0_0_2px_rgb(24_38_94/.12)] has-[:checked]:shadow-[inset_0_0_0_2px_var(--ink)]', done && 'cursor-default opacity-60')}>
                          <input type="radio" name={`${id}-builtin`} value={source.id} disabled={done} checked={builtinId === source.id} onChange={() => setBuiltinId(source.id)} className="mt-1 accent-riso-blue" />
                          <span className="grid min-w-0 gap-0.5">
                            <span className="text-[14px] font-black">
                              {localizedText(source.displayName)} <span className="font-mono text-[12px] font-bold text-ink-500">{t.plugins.idVersion(source.id, source.version)}</span>
                              {done && <span className="ml-2 text-[12px] font-bold text-ink-500">{t.plugins.installedBadge}</span>}
                            </span>
                            <span className="text-[13px] text-ink-700">{localizedText(source.description)}</span>
                          </span>
                        </label>
                      </li>
                    )
                  })}
                </ul>
              </fieldset>
            ) : (
              <div className="grid gap-1.5">
                <Label htmlFor={`${id}-location`}>{kind === 'git' ? text.gitUrl : text.localPath}</Label>
                <Input
                  id={`${id}-location`}
                  value={location}
                  aria-invalid={invalid ? true : undefined}
                  placeholder={kind === 'git' ? text.gitUrlPlaceholder : text.localPathPlaceholder}
                  onChange={(event) => setLocation(event.target.value)}
                  autoComplete="off"
                  spellCheck={false}
                />
                <p className="text-[12.5px] text-ink-500">{kind === 'git' ? text.gitHint : text.localHint}</p>
              </div>
            )}
            {kind === 'git' && (
              <div className="grid gap-1.5">
                <Label htmlFor={`${id}-ref`}>{text.gitRef}</Label>
                <Input id={`${id}-ref`} value={ref} placeholder={text.gitRefPlaceholder} onChange={(event) => setRef(event.target.value)} autoComplete="off" spellCheck={false} />
              </div>
            )}
            <FieldError message={invalid ?? undefined} />
            <ErrorAlert error={error} />
            <DialogFooter>
              <Button type="button" variant="secondary" disabled={installer.pending !== null} onClick={() => onOpenChange(false)}>
                {confirmText.cancel}
              </Button>
              <Button type="submit" disabled={installer.pending !== null}>
                {installer.pending !== null ? text.reading : text.read}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      {installer.dialog}
    </>
  )
}
