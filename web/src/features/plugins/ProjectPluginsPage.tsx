import { useState } from 'react'
import { Link } from 'react-router'
import { ChevronDownIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { usePlugin, usePluginConfig, useProjectPlugins, useSaveProjectPlugin } from '@/api/plugins'
import { ErrorAlert } from '@/components/ErrorAlert'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { SectionError } from '@/components/RequestError'
import { PageBody, Sheet } from '@/components/Sheet'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { useScope } from '@/features/projects/scope-context'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { t } from '@/i18n'
import { localizedText } from '@/lib/localized-text'
import { overviewPath, projectSettingsPath } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { AccountsManager } from './Accounts'
import { DeliveriesList } from './Activity'
import { hasAgentHalf } from './manifest'
import { IdVersion, InstalledSourceBadge, Quiet, StatusDot, surfaceListClass } from './parts'
import { pluginPath } from './paths'
import { PluginActionButtons } from './PluginActions'
import { ConfigForm } from './SchemaForm'
import { fieldsOf } from './schema-form'

const text = t.plugins.project

function SubTitle({ children }: { children: string }) {
  return <h3 className="text-[13px] font-semibold text-ink-700">{children}</h3>
}

// Mounted only when a project plugin row is expanded and has x-scope: project fields, so usePluginConfig isn't fetched until needed.
function ProjectPluginConfigSection({
  project,
  item,
  schema,
  save,
}: {
  project: Schemas['Project']
  item: Schemas['ProjectPlugin']
  schema: unknown
  save: ReturnType<typeof useSaveProjectPlugin>
}) {
  const global = usePluginConfig(item.pluginId)
  const list = useProjectPlugins(project.id)
  return (
    <section className="grid min-w-0 gap-2.5" aria-label={text.config}>
      <SubTitle>{text.config}</SubTitle>
      {global.data && (
        <ConfigForm
          formId={`project-plugin:${project.id}:${item.pluginId}`}
          schema={schema}
          mode="project"
          config={item.config}
          globalConfig={global.data}
          onSave={async (payload) => (await save.mutateAsync({ enabled: true, ...payload } as Schemas['ProjectPluginUpdate'])).config}
          reload={async () => (await list.refetch()).data?.plugins.find((plugin) => plugin.pluginId === item.pluginId)?.config}
        />
      )}
      <ErrorAlert error={global.error} />
    </section>
  )
}

// Lazy-mounted body of an expanded project plugin row: fetches usePlugin / usePluginConfig / DeliveriesList / AccountsManager ONLY once
// the user expands the row, eliminating N+1 queries across collapsed rows.
function ProjectPluginExpandedBody({
  project,
  item,
  save,
}: {
  project: Schemas['Project']
  item: Schemas['ProjectPlugin']
  save: ReturnType<typeof useSaveProjectPlugin>
}) {
  const detail = usePlugin(item.pluginId)
  const manifest = detail.data?.manifest
  const agentHalf = manifest ? hasAgentHalf(manifest) : false
  const platformHalf = manifest?.components.platform ?? false
  const hasProjectFields = manifest ? fieldsOf(manifest.configSchema, 'project').length > 0 : false
  const displayName = localizedText(item.displayName)

  return (
    <div className="flex min-w-0 flex-col gap-3.5 border-t border-hairline bg-surface-subtle/35 px-4 py-4">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        {detail.data && <InstalledSourceBadge plugin={detail.data.plugin} />}
        <IdVersion id={item.pluginId} version={item.version} />
        {manifest && (
          <span className="text-[12px] text-ink-500">
            {agentHalf && !platformHalf ? text.agentOnly : agentHalf ? null : t.plugins.noAgentHalf}
          </span>
        )}
      </div>
      <ErrorAlert error={detail.error} />
      {detail.isPending && <Quiet>{t.common.loading}</Quiet>}
      {manifest && !item.enabled && (
        <p className="text-[13px] text-ink-500">
          {agentHalf && !platformHalf ? text.agentOnly : text.enableToConfigure}
        </p>
      )}
      {item.enabled && manifest && (
        <>
          <PluginActionButtons
            plugin={{ id: item.pluginId, displayName }}
            actions={manifest.components.actions}
            page="project"
            where={{ projectId: project.id }}
          />
          <div className="grid min-w-0 gap-x-6 gap-y-4 md:grid-cols-2">
            <div className="grid min-w-0 content-start gap-4">
              {hasProjectFields && (
                <ProjectPluginConfigSection
                  project={project}
                  item={item}
                  schema={manifest.configSchema}
                  save={save}
                />
              )}
              {manifest.accounts.length > 0 && (
                <section className="grid min-w-0 gap-2.5" aria-label={t.plugins.detail.tabs.accounts}>
                  <AccountsManager projectId={project.id} pluginId={item.pluginId} kinds={manifest.accounts} />
                </section>
              )}
              {!hasProjectFields && manifest.accounts.length === 0 && <Quiet>{text.noProjectFields}</Quiet>}
            </div>
            <div className="grid min-w-0 content-start gap-2.5">
              <SubTitle>{text.here}</SubTitle>
              {(item.status === 'error' || item.status === 'host_stopped') && (
                <p className="rounded-input border border-red/20 bg-tint-red px-3 py-2 text-[13px] text-red-text">
                  <span className="font-semibold">
                    {item.status === 'error' ? text.pluginError : text.hostStopped}
                  </span>{' '}
                  · {text.keepsEnabled}
                </p>
              )}
              {platformHalf ? <DeliveriesList pluginId={item.pluginId} projectId={project.id} compact={3} /> : <Quiet>{text.agentOnly}</Quiet>}
            </div>
          </div>
        </>
      )}
    </div>
  )
}

// Compact ~52px collapsible accordion row per installed plugin in a project.
function ProjectPluginRow({ project, item }: { project: Schemas['Project']; item: Schemas['ProjectPlugin'] }) {
  const [expanded, setExpanded] = useState(false)
  const save = useSaveProjectPlugin(project.id, item.pluginId)
  const displayName = localizedText(item.displayName)
  const overrideCount = Object.keys(item.config.values as Record<string, unknown>).length + item.config.secretsSet.length
  const toggle = (enabled: boolean) => save.mutate({ enabled, values: item.config.values, expectedRevision: item.config.revision })

  return (
    <li className="min-w-0">
      <div className="flex min-h-[52px] flex-wrap items-center justify-between gap-x-3 gap-y-2 px-4 py-2.5">
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2.5 gap-y-1">
          <StatusDot status={item.status} muted={!item.enabled} />
          <h2 className="min-w-0 text-[14px] leading-snug font-semibold [overflow-wrap:anywhere]">
            <Link to={pluginPath(item.pluginId)} className="outline-none hover:underline focus-visible:shadow-focus">
              {displayName}
            </Link>
          </h2>
          <span className="font-mono text-[11.5px] text-ink-500">v{item.version}</span>
          {item.enabled && overrideCount > 0 && (
            <span className="rounded-stamp border border-hairline bg-surface-subtle px-1.5 py-px text-[11.5px] text-ink-700">
              {t.plugins.detail.overrides(overrideCount)}
            </span>
          )}
          {item.enabled && (item.status === 'error' || item.status === 'host_stopped') && (
            <span className="rounded-stamp bg-tint-red px-1.5 py-px text-[11.5px] font-semibold text-red-text">
              {item.status === 'error' ? text.pluginError : text.hostStopped}
            </span>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-3">
          <Switch
            aria-label={text.enable(displayName)}
            checked={item.enabled}
            disabled={save.isPending}
            onCheckedChange={toggle}
          />
          <Button
            type="button"
            size="xs"
            variant="ghost"
            aria-expanded={expanded}
            aria-label={`${expanded ? text.collapse : text.configure} ${displayName}`}
            onClick={() => setExpanded((open) => !open)}
          >
            <span>{expanded ? text.collapse : text.configure}</span>
            <ChevronDownIcon aria-hidden="true" className={cn('size-3.5 transition-transform', expanded && 'rotate-180')} />
          </Button>
        </div>
      </div>
      {save.error && (
        <div className="px-4 pb-2.5">
          <ErrorAlert error={save.error} />
        </div>
      )}
      {expanded && <ProjectPluginExpandedBody project={project} item={item} save={save} />}
    </li>
  )
}

// Embeddable inner accordion panel (without PageFrame / PageHeader / Sheet / DraftNavigationGuard) so ProjectSettingsPage can embed it
// directly inside its Plugins tab with zero duplication.
export function ProjectPluginsPanel({ project }: { project: Schemas['Project'] }) {
  const plugins = useProjectPlugins(project.id)
  const list = plugins.data?.plugins ?? []

  if (plugins.isError) return <SectionError title={text.title} error={plugins.error} onRetry={() => void plugins.refetch()} />
  if (plugins.isPending) return <Quiet>{t.plugins.loading}</Quiet>
  if (list.length === 0) {
    return (
      <div className="flex flex-col items-start gap-2.5 rounded-card border border-hairline bg-surface-subtle/50 px-5 py-5">
        <Quiet>{text.empty}</Quiet>
        <Button asChild size="xs" variant="secondary">
          <Link to="/plugins">{text.market}</Link>
        </Button>
      </div>
    )
  }

  return (
    <ul className={surfaceListClass}>
      {list.map((item) => (
        <ProjectPluginRow key={item.pluginId} project={project} item={item} />
      ))}
    </ul>
  )
}

// Standalone Project Plugins page wrapped in standard PageFrame + PageHeader + Sheet + PageBody.
export function ProjectPluginsPage() {
  const scope = useScope()
  if (scope.kind !== 'project') return null
  const project = scope.project
  return (
    <DraftNavigationGuard key={project.id} projectName={project.name}>
      <PageFrame>
        <PageHeader
          crumbs={[
            { label: project.name, to: overviewPath({ kind: 'project', name: project.name }) },
            { label: t.projects.settings.title, to: projectSettingsPath(project) },
          ]}
          title={text.title}
          actions={
            <Button asChild size="xs" variant="secondary">
              <Link to="/plugins">{text.market}</Link>
            </Button>
          }
        />
        <Sheet className="flex-1">
          <PageBody width="default" innerClassName="flex flex-col gap-4 pb-12">
            <p className="text-[13px] text-ink-700">{text.intro(project.name)}</p>
            <ProjectPluginsPanel project={project} />
          </PageBody>
        </Sheet>
      </PageFrame>
    </DraftNavigationGuard>
  )
}
