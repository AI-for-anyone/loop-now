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
import { cn } from '@/lib/utils'
import { pluginPath } from './paths'

type Manager = Schemas['ExternalIdentityManager']
type Candidate = Manager & { pluginName: string; channelLabel: string; status: Schemas['PluginStatus'] }
const text = t.plugins.manager
const keyOf = (manager: Manager | null) => (manager ? `${manager.pluginId}/${manager.channelId}` : '')

// 已安装插件里声明了 manageIdentities 的渠道（清单在插件详情里，与插件页共用缓存）。
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
          .map((channel) => ({ pluginId: detail.data.plugin.id, channelId: channel.id, pluginName: detail.data.plugin.displayName, channelLabel: channel.label, status: list.find((p) => p.id === detail.data.plugin.id)?.status ?? detail.data.plugin.status }))
      : [],
  )
  return { candidates, pending: plugins.isPending || details.some((detail) => detail.isPending), error: plugins.error ?? details.find((detail) => detail.error)?.error ?? null }
}

// 外部身份页的“代管”：选一个插件的渠道代管这个身份（发给它的消息与提问由插件推出去，人的回复以这个身份送回），或不代管。
// 代管插件不是运行中（停用、出错、宿主停止）时绑定保留，这里提示它没有在运行。
export function IdentityManager({ identity }: { identity: Schemas['ExternalIdentity'] }) {
  const id = useId()
  const save = useSetIdentityManager(identity.id)
  const { candidates, pending, error } = useCandidates()
  const current = identity.managedBy
  const [choice, setChoice] = useState(keyOf(current))
  const [base, setBase] = useState(keyOf(current))
  // 服务端的绑定变了（别处修改、卸载插件清空）且这里没有改动：跟着换。
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
    <section aria-labelledby={`${id}-title`} className="flex flex-col gap-3 rounded-panel bg-paper-raised px-6 py-[22px] shadow-print-rest">
      <h2 id={`${id}-title`} className="font-display-cn text-[26px] leading-tight font-normal">
        {text.title}
      </h2>
      <p className="text-[13.5px] text-ink-700">{text.intro}</p>
      {current && (
        <p role="status" className={cn('rounded-input px-3.5 py-2.5 text-[13.5px]', active && active.status !== 'active' ? 'bg-tint-red text-red-text' : 'bg-tint-blue text-ink')}>
          <span className="font-black">{text.current(active ? `${active.pluginName} · ${active.channelLabel}` : `${current.pluginId} · ${current.channelId}`)}</span>
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
