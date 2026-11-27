import { useId, useState } from 'react'
import { Link } from 'react-router'
import { useQueries } from '@tanstack/react-query'
import { api, call, type Schemas } from '@/api/client'
import { useSetIdentityManager } from '@/api/inbox'
import { pluginKeys, usePlugins } from '@/api/plugins'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { t } from '@/i18n'
import { selectClass } from '@/lib/field'
import { localizedText } from '@/lib/localized-text'
import { cn } from '@/lib/utils'
import { pluginPath } from './paths'

type Manager = Schemas['ExternalIdentityManager']
type Candidate = Manager & { pluginName: string; channelLabel: string; status: Schemas['PluginStatus'] }
const text = t.plugins.manager
const keyOf = (manager: Manager | null) => (manager ? `${manager.pluginId}/${manager.channelId}` : '')

// Channels of installed plugins that declare manageIdentities (the manifest is in the plugin details, sharing the cache with the plugin
// page).
function useCandidates() {
  const plugins = usePlugins()
  const list = plugins.data?.plugins ?? []
  const details = useQueries({
    queries: list.map((plugin) => ({
      queryKey: pluginKeys.detail(plugin.id),
      queryFn: () => call(api.GET('/plugins/{pluginId}', { params: { path: { pluginId: plugin.id } } })),
    })),
  })
  const candidates: Candidate[] = details.flatMap((detail) =>
    detail.data
      ? detail.data.manifest.components.channels
          .filter((channel) => channel.manageIdentities)
          .map((channel) => ({ pluginId: detail.data.plugin.id, channelId: channel.id, pluginName: localizedText(detail.data.plugin.displayName), channelLabel: localizedText(channel.label), status: list.find((p) => p.id === detail.data.plugin.id)?.status ?? detail.data.plugin.status }))
      : [],
  )
  return { candidates, pending: plugins.isPending || details.some((detail) => detail.isPending), error: plugins.error ?? details.find((detail) => detail.error)?.error ?? null }
}

// “代管” (Managed by) on the external identity page: choose a plugin channel to manage this identity (messages and questions sent to it
// are pushed out by the plugin, and people's replies come back as this identity), or none.
// When the managing plugin isn't running (disabled, errored, host stopped), the binding is kept, and a notice here says it isn't running.
export function IdentityManager({ identity }: { identity: Schemas['ExternalIdentity'] }) {
  const id = useId()
  const save = useSetIdentityManager(identity.id)
  const { candidates, pending, error } = useCandidates()
  const current = identity.managedBy
  const [choice, setChoice] = useState(keyOf(current))
  const [base, setBase] = useState(keyOf(current))
  // The server's binding changed (edited elsewhere, or cleared by uninstalling the plugin) and nothing was changed here: follow it.
  if (keyOf(current) !== base) {
    if (choice === base) setChoice(keyOf(current))
    setBase(keyOf(current))
  }
  const active = current ? candidates.find((c) => keyOf(c) === keyOf(current)) : undefined
  const submit = () => {
    const next = candidates.find((c) => keyOf(c) === choice)
    save.mutate(next ? { pluginId: next.pluginId, channelId: next.channelId } : null)
  }
  return (
    <section aria-labelledby={`${id}-title`} className="flex min-w-0 flex-col gap-3.5">
      <h3 id={`${id}-title`} className="text-[15px] leading-[1.3] font-semibold">
        {text.title}
      </h3>
      <p className="text-[13.5px] text-ink-700">{text.intro}</p>
      {current && (
        <p role="status" className={cn('rounded-input px-3.5 py-2.5 text-[13.5px]', active && active.status !== 'active' ? 'border border-red/20 bg-tint-red text-red-text' : 'border border-hairline bg-surface-subtle text-ink')}>
          <span className="font-semibold">{text.current(active ? `${active.pluginName} · ${active.channelLabel}` : `${current.pluginId} · ${current.channelId}`)}</span>
          {active && active.status !== 'active' && <> · {text.notRunning(t.plugins.status[active.status])}</>}
          {active && (
            <>
              {' '}
              <Link to={pluginPath(active.pluginId)} className="font-bold underline">
                {text.openPlugin}
              </Link>
            </>
          )}
        </p>
      )}
      <ErrorAlert error={error} />
      {!pending && candidates.length === 0 ? (
        <p className="text-[13.5px] text-ink-700">
          {text.none}{' '}
          <Link to="/plugins" className="font-bold text-riso-blue underline">
            {t.plugins.title}
          </Link>
        </p>
      ) : (
        <div className="flex min-w-0 flex-wrap items-end gap-2.5">
          <div className="grid min-w-0 flex-1 basis-[260px] gap-1.5">
            <Label htmlFor={`${id}-manager`}>{text.label}</Label>
            <select id={`${id}-manager`} value={choice} disabled={pending || save.isPending} onChange={(event) => setChoice(event.target.value)} className={selectClass}>
              <option value="">{text.noManager}</option>
              {candidates.map((candidate) => (
                <option key={keyOf(candidate)} value={keyOf(candidate)}>
                  {text.option(candidate.pluginName, candidate.channelLabel)}
                  {candidate.status !== 'active' ? text.optionNotRunning : ''}
                </option>
              ))}
            </select>
          </div>
          <Button size="sm" disabled={pending || save.isPending || choice === keyOf(current)} onClick={submit}>
            {save.isPending ? t.plugins.form.saving : text.save}
          </Button>
        </div>
      )}
      <ErrorAlert error={save.error} />
    </section>
  )
}
