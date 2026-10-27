import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { usePlugin, usePluginConfig, useProjectPlugins, useSaveProjectPlugin } from '@/api/plugins'
import { ErrorAlert } from '@/components/ErrorAlert'
import { SectionError } from '@/components/RequestError'
import { Switch } from '@/components/ui/switch'
import { useScope } from '@/features/projects/scope-context'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { t } from '@/i18n'
import { pluginText } from '@/i18n/plugins'
import { pageTitleClass } from '@/lib/page-title'
import { cn } from '@/lib/utils'
import { AccountsManager } from './Accounts'
import { DeliveriesList } from './Activity'
import { hasAgentHalf } from './manifest'
import { cardClass, IdVersion, Quiet, SourceBadge } from './parts'
import { pluginPath } from './paths'
import { ConfigForm } from './SchemaForm'
import { PluginActionButtons } from './PluginActions'
import { fieldsOf } from './schema-form'

const text = pluginText.project

function SubTitle({ children }: { children: string }) {
  return <h3 className="text-[13px] font-black text-ink-700">{children}</h3>
}

// 项目里的一个插件：启用开关；启用后是项目级配置（只有 x-scope: project 的项）、账号与这个项目里的事件。
function ProjectPluginCard({ project, item }: { project: Schemas['Project']; item: Schemas['ProjectPlugin'] }) {
  const detail = usePlugin(item.pluginId)
  const global = usePluginConfig(item.pluginId)
  const list = useProjectPlugins(project.id)
  const save = useSaveProjectPlugin(project.id, item.pluginId)
  const manifest = detail.data?.manifest
  const agentHalf = manifest ? hasAgentHalf(manifest) : false
  const platformHalf = manifest?.components.platform ?? false
  const toggle = (enabled: boolean) => save.mutate({ enabled, values: item.config.values, expectedRevision: item.config.revision })
  const hasProjectFields = manifest ? fieldsOf(manifest.configSchema, 'project').length > 0 : false
  return (
    <li className={cn('flex min-w-0 flex-col gap-3 px-4 py-3.5', item.enabled ? cn(cardClass, 'shadow-[3px_3px_0_var(--riso-blue)]') : 'rounded-panel border-2 border-dashed border-ink/60')}>
      <div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-2">
        <h2 className="font-display-cn text-[21px] leading-tight font-normal">
          <Link to={pluginPath(item.pluginId)} className="outline-none hover:underline focus-visible:shadow-focus">
            {item.displayName}
          </Link>
        </h2>
        {detail.data && <SourceBadge source={detail.data.plugin.source} />}
        <IdVersion id={item.pluginId} version={item.version} />
        {manifest && <span className="text-[12.5px] text-ink-700">{agentHalf && !platformHalf ? text.agentOnly : agentHalf ? null : pluginText.noAgentHalf}</span>}
        <label className="ml-auto inline-flex items-center gap-2 text-[13px] font-black">
          <Switch aria-label={text.enable(item.displayName)} checked={item.enabled} disabled={save.isPending} onCheckedChange={toggle} />
          {item.enabled ? text.enabled : text.disabled}
        </label>
      </div>
      <ErrorAlert error={save.error ?? detail.error} />
      {item.enabled && manifest && <PluginActionButtons plugin={{ id: item.pluginId, displayName: item.displayName }} actions={manifest.components.actions} page="project" where={{ projectId: project.id }} />}
      {item.enabled && manifest && (
        <div className="grid min-w-0 gap-x-6 gap-y-4 border-t border-ink/10 pt-3.5 md:grid-cols-2">
          <div className="grid min-w-0 content-start gap-4">
            {hasProjectFields && (
              <section className="grid min-w-0 gap-2.5" aria-label={text.config}>
                <SubTitle>{text.config}</SubTitle>
                {global.data && (
                  <ConfigForm
                    formId={`project-plugin:${project.id}:${item.pluginId}`}
                    schema={manifest.configSchema}
                    mode="project"
                    config={item.config}
                    globalConfig={global.data}
                    onSave={async (payload) => (await save.mutateAsync({ enabled: true, ...payload } as Schemas['ProjectPluginUpdate'])).config}
                    reload={async () => (await list.refetch()).data?.plugins.find((plugin) => plugin.pluginId === item.pluginId)?.config}
                  />
                )}
                <ErrorAlert error={global.error} />
              </section>
            )}
            {manifest.accounts.length > 0 && (
              <section className="grid min-w-0 gap-2.5" aria-label={pluginText.detail.tabs.accounts}>
                <AccountsManager projectId={project.id} pluginId={item.pluginId} kinds={manifest.accounts} />
              </section>
            )}
            {!hasProjectFields && manifest.accounts.length === 0 && <Quiet>{text.noProjectFields}</Quiet>}
          </div>
          <div className="grid min-w-0 content-start gap-2.5">
            <SubTitle>{text.here}</SubTitle>
            {(item.status === 'error' || item.status === 'host_stopped') && (
              <p className="rounded-input bg-tint-red px-3 py-2 text-[13px] text-red-text">
                <span className="font-black">{item.status === 'error' ? text.pluginError : text.hostStopped}</span> · {text.keepsEnabled}
              </p>
            )}
            {platformHalf ? <DeliveriesList pluginId={item.pluginId} projectId={project.id} compact={3} /> : <Quiet>{text.agentOnly}</Quiet>}
          </div>
        </div>
      )}
    </li>
  )
}

// 项目插件页：这个项目里每个已安装插件一张卡片，启用后填写项目级配置与账号。给 Agent 用的部分在 Agent 的配置页里授权。
export function ProjectPluginsPage() {
  const scope = useScope()
  return scope.kind === 'project' ? <ProjectPlugins key={scope.project.id} project={scope.project} /> : null
}

function ProjectPlugins({ project }: { project: Schemas['Project'] }) {
  const plugins = useProjectPlugins(project.id)
  const list = plugins.data?.plugins ?? []
  return (
    <DraftNavigationGuard projectName={project.name}>
      <div className="h-full overflow-y-auto">
        <div className="mx-auto flex max-w-[1180px] flex-col gap-4 px-4 pt-[22px] pb-16 sm:px-8">
          <header className="flex flex-col gap-1.5">
            <span className="text-[12px] font-bold text-ink-500">
              <Link to={`/p/${encodeURIComponent(project.name)}/settings`} className="hover:text-ink hover:underline">
                {`${project.name} › ${t.projects.settings.title}`}
              </Link>
            </span>
            <h1 className={pageTitleClass}>{text.title}</h1>
          </header>
          <p className="text-[13.5px] text-ink-700">
            {text.intro(project.name)}{' '}
            <Link to="/plugins" className="font-bold text-riso-blue underline">
              {text.market}
            </Link>
          </p>
          {plugins.isError ? (
            <SectionError title={text.title} error={plugins.error} onRetry={() => void plugins.refetch()} />
          ) : plugins.isPending ? (
            <Quiet>{pluginText.loading}</Quiet>
          ) : list.length === 0 ? (
            <Quiet>{text.empty}</Quiet>
          ) : (
            <ul className="flex flex-col gap-3.5">
              {list.map((item) => (
                <ProjectPluginCard key={item.pluginId} project={project} item={item} />
              ))}
            </ul>
          )}
        </div>
      </div>
    </DraftNavigationGuard>
  )
}
