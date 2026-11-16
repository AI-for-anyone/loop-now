import { useState, type FormEvent } from 'react'
import { ApiError, type Schemas } from '@/api/client'
import { useInvokePluginAction, usePlugin, usePlugins, useProjectPlugins } from '@/api/plugins'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { t } from '@/i18n'
import { localizedText } from '@/lib/localized-text'
import { toastStore } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { SchemaFields } from './SchemaForm'
import { fieldsOf, initialDraft, serverFieldErrors, toPayload, validate } from './schema-form'

type Action = Schemas['PluginAction']
type Where = Schemas['PluginActionInput']['where']
export type ActionPage = Action['where'][number]
const text = t.plugins.actions

// The form dialog for one action: generated from the action's fields; with no fields it just asks for confirmation. The result shows as a
// toast, and if it carries a link, the link also opens in a new window.
function ActionDialog({ plugin, action, where, onClose }: { plugin: { id: string; displayName: string }; action: Action; where: Where; onClose: () => void }) {
  const fields = fieldsOf(action.fields)
  const invoke = useInvokePluginAction(plugin.id, action.id)
  const [draft, setDraft] = useState(() => initialDraft(fields, { values: {}, secretsSet: [], revision: 0 }))
  const [errors, setErrors] = useState<Record<string, string>>({})
  const submit = (event: FormEvent) => {
    event.preventDefault()
    const local = validate(fields, draft)
    setErrors(local)
    if (Object.keys(local).length) return
    // The action's input goes to the plugin as a whole: fields marked x-secret are also put into input as ordinary values (they are just
    // not echoed while typing).
    const { values, secrets } = toPayload(fields, draft, {})
    const input = { ...values, ...Object.fromEntries(Object.entries(secrets ?? {}).filter(([, value]) => value !== null)) }
    invoke.mutate(
      { where, input: input as never },
      {
        onSuccess: (result) => {
          toastStore.show(result.notice, result.url ? { href: result.url, label: text.open } : undefined)
          if (result.url) window.open(result.url, '_blank', 'noopener,noreferrer')
          onClose()
        },
        onError: (error) => setErrors(serverFieldErrors(fields, error.fields)),
      },
    )
  }
  const notEnabled = invoke.error instanceof ApiError && invoke.error.code === 'plugin_not_enabled'
  return (
    <Dialog open onOpenChange={(open) => !open && !invoke.isPending && onClose()}>
      <DialogContent kind="action" className="sm:max-w-[520px]">
        <DialogHeader>
          <DialogTitle>{action.label}</DialogTitle>
          <DialogDescription>{text.by(plugin.displayName)}</DialogDescription>
        </DialogHeader>
        <form className="grid min-w-0 gap-4" onSubmit={submit} noValidate>
          {fields.length ? <SchemaFields fields={fields} draft={draft} onChange={setDraft} errors={errors} disabled={invoke.isPending} /> : <p className="text-[14px]">{text.confirm(action.label)}</p>}
          {notEnabled ? <p role="status" className="rounded-input bg-tint-yellow px-3 py-2 text-[13px] font-bold">{text.notEnabled}</p> : <ErrorAlert error={invoke.error} />}
          <DialogFooter>
            <Button type="button" variant="secondary" disabled={invoke.isPending} onClick={onClose}>
              {t.plugins.confirm.cancel}
            </Button>
            <Button type="submit" disabled={invoke.isPending}>
              {invoke.isPending ? text.running : text.run}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// One plugin's action buttons on this kind of page (the actions in the manifest whose where includes this kind of page).
export function PluginActionButtons({ plugin, actions, page, where, className }: { plugin: { id: string; displayName: string }; actions: Action[]; page: ActionPage; where: Where; className?: string }) {
  const [open, setOpen] = useState<Action | null>(null)
  const mine = actions.filter((action) => action.where.includes(page))
  if (!mine.length) return null
  return (
    <div role="group" aria-label={text.group(plugin.displayName)} className={cn('flex flex-wrap gap-2', className)}>
      {mine.map((action) => (
        <Button key={action.id} type="button" size="xs" variant="secondary" title={text.by(plugin.displayName)} onClick={() => setOpen(action)}>
          {action.label}
        </Button>
      ))}
      {open && <ActionDialog plugin={plugin} action={open} where={where} onClose={() => setOpen(null)} />}
    </div>
  )
}

function OnePlugin({ pluginId, page, where }: { pluginId: string; page: ActionPage; where: Where }) {
  const detail = usePlugin(pluginId)
  if (!detail.data) return null
  return <PluginActionButtons plugin={{ id: detail.data.plugin.id, displayName: localizedText(detail.data.plugin.displayName) }} actions={detail.data.manifest.components.actions} page={page} where={where} />
}

// Used by the task page and the external identity page: lists the actions of all installed plugins on this kind of page. When projectId
// is given, only plugins enabled in that project are listed.
export function PagePluginActions({ page, where, projectId, className }: { page: ActionPage; where: Where; projectId?: string; className?: string }) {
  const plugins = usePlugins()
  const project = useProjectPlugins(projectId ?? '')
  const enabled = projectId ? new Set((project.data?.plugins ?? []).filter((item) => item.enabled).map((item) => item.pluginId)) : null
  const list = (plugins.data?.plugins ?? []).filter((plugin) => (enabled ? enabled.has(plugin.id) : true))
  if (!list.length) return null
  return (
    <div className={cn('flex flex-wrap items-center gap-2', className)}>
      {list.map((plugin) => (
        <OnePlugin key={plugin.id} pluginId={plugin.id} page={page} where={where} />
      ))}
    </div>
  )
}

