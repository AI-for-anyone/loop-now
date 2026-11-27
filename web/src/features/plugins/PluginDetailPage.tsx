import { useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import { useQueries } from '@tanstack/react-query'
import { ChevronDownIcon, ChevronRightIcon } from 'lucide-react'
import { api, call, type Schemas } from '@/api/client'
import {
  marketEntryOf,
  marketSource,
  pluginKeys,
  usePlugin,
  usePluginConfig,
  usePluginMarkets,
  usePluginReadme,
  useSavePluginConfig,
  useSaveProjectPlugin,
  useUninstallPlugin,
} from '@/api/plugins'
import { useProjects } from '@/api/queries'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { IndexTabs } from '@/components/IndexTabs'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { SectionError } from '@/components/RequestError'
import { PageBody, Sheet } from '@/components/Sheet'
import { Button } from '@/components/ui/button'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { t } from '@/i18n'
import { isNotFound } from '@/lib/errors'
import { indexTabClass } from '@/lib/index-tab'
import { localizedText } from '@/lib/localized-text'
import { cn } from '@/lib/utils'
import { AccountsManager } from './Accounts'
import { ActivityPanel, type ActivityView } from './Activity'
import { Chips, ManifestSummary } from './ManifestSummary'
import { componentChips } from './manifest'
import { cardClass, IdVersion, InstalledSourceBadge, PanelTitle, Quiet, RepositoryLink, StatusLabel, surfaceListClass } from './parts'
import { pluginPath } from './paths'
import { PluginActionButtons } from './PluginActions'
import { PluginProviders } from './PluginProviders'
import { ProjectEnableList } from './ProjectEnable'
import { ReadmeMarkdown, TruncatedNote } from './Readme'
import { repositoryUrl } from './repository'
import { ConfigForm } from './SchemaForm'
import { describeValue, fieldsOf } from './schema-form'
import { usePluginInstaller } from './usePluginInstaller'

const text = t.plugins
const copy = text.detail

type Detail = Schemas['PluginDetail']
type DetailTab = 'overview' | 'config' | 'activity'

function parseActivityView(value: string | null): ActivityView {
  if (value === 'records' || value === 'logs') return value
  return 'events'
}

// Resolve canonical tab + sub-view while preserving backward-compatible route aliases:
// /accounts → config (expanding per-project rows), /events | /records | /logs → activity with matching sub-view.
function resolveRoute(tabParam: string | undefined, viewParam: string | null): {
  tab: DetailTab
  activityView: ActivityView
  expandProjects: boolean
} {
  if (tabParam === 'config') return { tab: 'config', activityView: 'events', expandProjects: false }
  if (tabParam === 'accounts') return { tab: 'config', activityView: 'events', expandProjects: true }
  if (tabParam === 'activity') return { tab: 'activity', activityView: parseActivityView(viewParam), expandProjects: false }
  if (tabParam === 'events') return { tab: 'activity', activityView: 'events', expandProjects: false }
  if (tabParam === 'records') return { tab: 'activity', activityView: 'records', expandProjects: false }
  if (tabParam === 'logs') return { tab: 'activity', activityView: 'logs', expandProjects: false }
  return { tab: 'overview', activityView: 'events', expandProjects: false }
}

// Whether there is anything to say about model providers: it has Pi extensions, or providers were read, or errors occurred.
const hasProviderInfo = (detail: Detail) =>
  detail.manifest.components.agentExtensions > 0 || detail.providers.length > 0 || detail.providerErrors.length > 0 || detail.providersError !== null

// Tab 1: Overview — compact 2-column top summary grid (Enabled in projects | Capabilities & security) plus README prose below.
function OverviewTab({ detail }: { detail: Detail }) {
  const id = detail.plugin.id
  const { manifest } = detail
  const readme = usePluginReadme(id)
  const showReadme = !readme.isPending && readme.data?.file !== null
  const showProviders = hasProviderInfo(detail)
  const hasProviderErrors = detail.providersError !== null || detail.providerErrors.length > 0
  const chips = componentChips(manifest)

  return (
    <div className="flex min-w-0 flex-col gap-5">
      <div className="grid min-w-0 gap-5 lg:grid-cols-2 lg:items-start">
        <section aria-label={copy.enabledIn} className={cn(cardClass, 'flex min-w-0 flex-col gap-3 rounded-card px-4 py-3.5')}>
          <PanelTitle title={copy.enabledIn} hint={copy.enabledInHint} />
          <ProjectEnableList pluginId={id} schema={manifest.configSchema} />
        </section>

        <section aria-label={copy.capabilities} className={cn(cardClass, 'flex min-w-0 flex-col gap-3 rounded-card px-4 py-3.5')}>
          <PanelTitle title={copy.capabilities} />
          {chips.length > 0 ? (
            <Chips items={chips} />
          ) : (
            <Quiet>{text.confirm.none}</Quiet>
          )}
          {showProviders && (
            <div className="flex flex-wrap items-center gap-2 text-[12.5px] text-ink-700">
              <span className="font-medium text-ink">{text.providers.title}:</span>
              {detail.providersError !== null ? (
                <span className="font-medium text-red-text">{text.providers.failed(detail.providersError)}</span>
              ) : detail.providers.length > 0 ? (
                <span>{detail.providers.map((p) => p.name || p.id).join(', ')}</span>
              ) : (
                <span className="text-ink-500">{text.providers.none}</span>
              )}
            </div>
          )}
          {manifest.failClosed && (
            <p className="text-[12.5px] font-semibold text-red-text">{text.confirm.failClosed}</p>
          )}
          <details open={hasProviderErrors || undefined} className="group border-t border-hairline pt-2.5">
            <summary className="flex cursor-pointer list-none items-center gap-1.5 text-[12.5px] font-semibold text-ink-700 outline-none hover:text-ink focus-visible:shadow-focus">
              <ChevronRightIcon aria-hidden="true" className="size-3.5 shrink-0 transition-transform group-open:rotate-90" />
              <span>{copy.inspectManifest}</span>
            </summary>
            <div className="mt-3 flex min-w-0 flex-col gap-4">
              <ManifestSummary manifest={manifest} columns={false} layout="table" />
              {showProviders && (
                <section aria-label={text.providers.title} className="flex min-w-0 flex-col gap-2 border-t border-hairline pt-3.5">
                  <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
                    <h3 className="text-[13.5px] font-semibold">{text.providers.title}</h3>
                    <span className="text-[12px] text-ink-500">{text.providers.hint}</span>
                  </div>
                  <PluginProviders detail={detail} />
                </section>
              )}
            </div>
          </details>
        </section>
      </div>

      {showReadme && (
        <section aria-label={t.plugins.readme.title} className={cn(cardClass, 'flex min-w-0 flex-col gap-3 rounded-card px-5 py-4')}>
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-[15px] font-semibold tracking-tight">
              {t.plugins.readme.title}
            </h2>
            {readme.data?.file && (
              <span className="rounded-stamp border border-hairline bg-surface-subtle px-2 py-0.5 font-mono text-[11.5px] text-ink-700">
                {readme.data.file}
              </span>
            )}
          </div>
          <ErrorAlert error={readme.error} />
          {readme.data && <ReadmeMarkdown readme={readme.data} />}
          {readme.data?.truncated && <TruncatedNote />}
        </section>
      )}
    </div>
  )
}

// Expandable per-project overrides & accounts row inside the Configuration tab, allowing in-place editing of both x-scope: project
// fields and project-scoped plugin accounts.
function ProjectConfigRow({
  project,
  item,
  detail,
  globalConfig,
  hasProjectFields,
  hasAccounts,
  defaultExpanded,
  onReloadProject,
}: {
  project: Schemas['Project']
  item: Schemas['ProjectPlugin']
  detail: Detail
  globalConfig: Schemas['PluginConfig'] | undefined
  hasProjectFields: boolean
  hasAccounts: boolean
  defaultExpanded: boolean
  onReloadProject: () => Promise<Schemas['PluginConfig'] | undefined>
}) {
  const [expanded, setExpanded] = useState(defaultExpanded)
  const save = useSaveProjectPlugin(project.id, detail.plugin.id)
  const values = item.config.values as Record<string, unknown>
  const projectFields = fieldsOf(detail.manifest.configSchema, 'project')
  const overriddenFields = projectFields.filter((field) => values[field.name] !== undefined || item.config.secretsSet.includes(field.name))

  return (
    <li className="min-w-0">
      <div className="flex min-h-[48px] flex-wrap items-center justify-between gap-x-3 gap-y-1.5 px-4 py-2.5">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <span className="truncate text-[13.5px] font-semibold text-ink">{project.name}</span>
            {hasProjectFields && (
              <span className="text-[12px] text-ink-500">
                {overriddenFields.length ? text.config.overridden(overriddenFields.length) : text.config.useGlobal}
              </span>
            )}
          </div>
          {!expanded && overriddenFields.length > 0 && (
            <p className="truncate text-[12px] text-ink-500">
              {overriddenFields
                .map((field) => `${field.label} → ${field.secret ? t.plugins.form.secretSet : describeValue(field, values[field.name])}`)
                .join(' · ')}
            </p>
          )}
        </div>
        <Button
          type="button"
          size="xs"
          variant="ghost"
          aria-expanded={expanded}
          onClick={() => setExpanded((open) => !open)}
        >
          <span>{expanded ? t.plugins.project.collapse : copy.configure}</span>
          <ChevronDownIcon aria-hidden="true" className={cn('size-3.5 transition-transform', expanded && 'rotate-180')} />
        </Button>
      </div>
      {expanded && (
        <div className="flex min-w-0 flex-col gap-5 border-t border-hairline bg-surface-subtle/35 px-4 py-4">
          {hasProjectFields && globalConfig && (
            <section aria-label={t.plugins.project.config} className="grid min-w-0 gap-2.5">
              <h3 className="text-[13px] font-semibold text-ink-700">{t.plugins.project.config}</h3>
              <ConfigForm
                formId={`plugin-detail-project:${project.id}:${detail.plugin.id}`}
                schema={detail.manifest.configSchema}
                mode="project"
                config={item.config}
                globalConfig={globalConfig}
                onSave={async (payload) => (await save.mutateAsync({ enabled: true, ...payload } as Schemas['ProjectPluginUpdate'])).config}
                reload={onReloadProject}
              />
            </section>
          )}
          {hasAccounts && (
            <AccountsManager projectId={project.id} pluginId={detail.plugin.id} kinds={detail.manifest.accounts} />
          )}
          <ErrorAlert error={save.error} />
        </div>
      )}
    </li>
  )
}

// Tab 2: Configuration — Global config + Per-project overrides & accounts (editable in place, with notEnabledProjects empty state) +
// collapsible Runtime environment details.
function ConfigTab({ detail, expandProjects }: { detail: Detail; expandProjects: boolean }) {
  const id = detail.plugin.id
  const config = usePluginConfig(id)
  const save = useSavePluginConfig(id)
  const projects = useProjects()
  const projectList = projects.data?.projects ?? []
  const allFields = fieldsOf(detail.manifest.configSchema)
  const hasProjectFields = fieldsOf(detail.manifest.configSchema, 'project').length > 0
  const hasAccounts = detail.manifest.accounts.length > 0
  const showProjectSection = hasProjectFields || hasAccounts
  const scopes = detail.manifest.permissions

  const projectQueries = useQueries({
    queries: showProjectSection
      ? projectList.map((project) => ({
          queryKey: pluginKeys.project(project.id),
          queryFn: () => call(api.GET('/projects/{projectId}/plugins', { params: { path: { projectId: project.id } } })),
        }))
      : [],
  })

  const enabledEntries = projectList
    .map((project, index) => {
      const query = projectQueries[index]
      const item = query?.data?.plugins.find((plugin) => plugin.pluginId === id)
      return item?.enabled ? { project, item, refetch: query.refetch } : null
    })
    .filter((entry): entry is NonNullable<typeof entry> => entry !== null)
  const projectsStillLoading = projects.isPending || projectQueries.some((query) => query.isPending)

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <section aria-label={text.config.global} className="flex min-w-0 flex-col gap-3">
        {allFields.length === 0 ? (
          <div className="rounded-card border border-hairline bg-surface-subtle/50 px-4 py-3 text-[13px] text-ink-500">
            {text.config.none}
          </div>
        ) : (
          <div className={cn(cardClass, 'flex min-w-0 flex-col gap-3 rounded-card px-4 py-4')}>
            <PanelTitle title={text.config.global} hint={text.config.globalHint} />
            <ErrorAlert error={config.error} />
            {config.data && (
              <ConfigForm
                formId={`plugin-config:${id}`}
                schema={detail.manifest.configSchema}
                config={config.data}
                onSave={(payload) => save.mutateAsync(payload as Schemas['PluginConfigUpdate'])}
                reload={async () => (await config.refetch()).data}
                footer={text.config.savedNotice}
              />
            )}
          </div>
        )}
      </section>

      {showProjectSection && (
        <section aria-label={copy.projectSettings} className="flex min-w-0 flex-col gap-3">
          <PanelTitle title={copy.projectSettings} hint={copy.projectSettingsHint} />
          <ErrorAlert error={projects.error} />
          {projectsStillLoading ? (
            <Quiet>{t.common.loading}</Quiet>
          ) : enabledEntries.length === 0 ? (
            <div className="rounded-card border border-hairline bg-surface-subtle/50 px-4 py-3.5 text-[13px] text-ink-500">
              {text.accounts.notEnabledProjects}
            </div>
          ) : (
            <ul className={surfaceListClass}>
              {enabledEntries.map(({ project, item, refetch }) => (
                <ProjectConfigRow
                  key={project.id}
                  project={project}
                  item={item}
                  detail={detail}
                  globalConfig={config.data}
                  hasProjectFields={hasProjectFields}
                  hasAccounts={hasAccounts}
                  defaultExpanded={expandProjects}
                  onReloadProject={async () => (await refetch()).data?.plugins.find((plugin) => plugin.pluginId === id)?.config}
                />
              ))}
            </ul>
          )}
        </section>
      )}

      <details className="group rounded-card border border-hairline bg-surface-subtle/50 px-4 py-2.5">
        <summary className="flex cursor-pointer list-none items-center gap-1.5 text-[13px] font-semibold text-ink-700 outline-none hover:text-ink focus-visible:shadow-focus">
          <ChevronRightIcon aria-hidden="true" className="size-3.5 shrink-0 transition-transform group-open:rotate-90" />
          <span>{text.config.env}</span>
        </summary>
        <section aria-label={text.config.env} className="mt-2.5">
          <pre className="overflow-x-auto no-scrollbar rounded-input border border-code-border bg-code-bg px-3 py-2.5 font-mono text-[12px] leading-[1.7] whitespace-pre-wrap text-code-fg [overflow-wrap:anywhere]">
            {[
              `LOOP_NOW_PLUGIN_ROOT=${detail.installDir}`,
              `LOOP_NOW_PLUGIN_DATA=${detail.dataDir}`,
              `lnp_… ${scopes.length ? text.config.tokenScopes(text.confirm.list(scopes)) : text.config.noScopes}`,
            ].join('\n')}
          </pre>
        </section>
      </details>
    </div>
  )
}

// Update: a plugin installed from a market goes by the market's version (clickable only when there is a new version); other sources fetch
// a new preview from the recorded source to see whether there is a new version.
function UpdateButton({ plugin, installer }: { plugin: Schemas['Plugin']; installer: ReturnType<typeof usePluginInstaller> }) {
  const markets = usePluginMarkets()
  const busy = installer.pending !== null
  if (!plugin.source.market)
    return (
      <Button size="xs" disabled={busy} onClick={() => void installer.begin('update', { pluginId: plugin.id })}>
        {busy ? copy.checking : text.update}
      </Button>
    )
  const entry = marketEntryOf(markets.data?.markets, plugin)
  if (!entry?.updateAvailable)
    return (
      <Button size="xs" variant="secondary" disabled className="bg-paper-tab text-ink-500 disabled:opacity-100">
        {text.markets.upToDate}
      </Button>
    )
  return (
    <Button size="xs" disabled={busy} onClick={() => void installer.begin('update', { source: marketSource(plugin.source.market!, entry) })}>
      {busy ? copy.checking : text.markets.updateTo(entry.version)}
    </Button>
  )
}

const DETAIL_TABS: readonly DetailTab[] = ['overview', 'config', 'activity']

// Plugin details: compact PageHeader, 3 unified SubNav tabs (Overview, Configuration, Activity & Logs), and PageBody with floating
// ScrollRuler (no 196px right-hand SectionToc stub).
export function PluginDetailPage() {
  const { pluginId = '', tab: tabParam } = useParams()
  const [search] = useSearchParams()
  const navigate = useNavigate()
  const { tab, activityView, expandProjects } = resolveRoute(tabParam, search.get('view'))
  const query = usePlugin(pluginId)
  usePluginMarkets()
  const uninstall = useUninstallPlugin(pluginId)
  const installer = usePluginInstaller()
  const [removing, setRemoving] = useState(false)
  const [deleteData, setDeleteData] = useState(false)
  const detail = query.data

  if (query.isError)
    return (
      <PageFrame>
        <PageHeader crumbs={[{ label: t.shell.global }, { label: copy.crumb, to: '/plugins' }]} title={pluginId || copy.crumb} />
        <Sheet className="flex-1">
          <PageBody width="default" innerClassName="flex flex-col gap-3">
            {isNotFound(query.error) ? (
              <p role="status" className="text-[14.5px] font-semibold">
                {copy.notFound}
              </p>
            ) : (
              <SectionError title={copy.crumb} error={query.error} onRetry={() => void query.refetch()} />
            )}
            <Link to="/plugins" className="self-start text-[13px] font-medium text-riso-blue underline outline-none focus-visible:shadow-focus">
              {copy.back}
            </Link>
          </PageBody>
        </Sheet>
      </PageFrame>
    )

  if (!detail) return <Quiet>{text.loading}</Quiet>

  const { plugin, manifest } = detail
  const repository = plugin.source.kind === 'git' ? { location: plugin.source.location, href: repositoryUrl(plugin.source.location, plugin.source.ref) } : null
  const name = localizedText(plugin.displayName)

  return (
    <DraftNavigationGuard>
      <PageFrame>
        <PageHeader
          crumbs={[{ label: t.shell.global }, { label: copy.crumb, to: '/plugins' }]}
          current={plugin.id}
          title={name}
          actions={
            <>
              <PluginActionButtons plugin={{ id: plugin.id, displayName: name }} actions={manifest.components.actions} page="global" where={{}} />
              <UpdateButton plugin={plugin} installer={installer} />
              <Button size="xs" variant="danger-outline" className="px-4" onClick={() => setRemoving(true)}>
                {text.uninstall}
              </Button>
            </>
          }
          note={
            <>
              {[localizedText(plugin.description), text.from[plugin.source.kind], copy.installedOn(t.chat.time(plugin.installedAt))].filter(Boolean).join(' · ')}
              {repository && (
                <>
                  {' · '}
                  {repository.href ? <RepositoryLink href={repository.href} /> : <span className="font-mono text-[12px]">{repository.location}</span>}
                </>
              )}
            </>
          }
        >
          <InstalledSourceBadge plugin={plugin} />
          <IdVersion id={plugin.id} version={plugin.version} />
          <StatusLabel status={plugin.status} />
        </PageHeader>
        <IndexTabs aria-label={name}>
          {DETAIL_TABS.map((value) => (
            <Link
              key={value}
              to={pluginPath(plugin.id, value === 'overview' ? undefined : value)}
              aria-current={value === tab ? 'page' : undefined}
              className={cn(indexTabClass(value === tab), 'max-sm:px-3')}
            >
              {copy.tabs[value]}
            </Link>
          ))}
        </IndexTabs>

        <Sheet className="flex-1">
          <PageBody width="default" innerClassName="flex flex-col gap-5 pb-12">
            <ErrorAlert error={installer.errorFor('update')} />
            {plugin.status === 'error' && plugin.lastError && (
              <p role="alert" className="rounded-input border border-red/20 bg-tint-red px-3.5 py-2.5 text-[13.5px] font-medium text-red-text [overflow-wrap:anywhere]">
                {plugin.lastError}
              </p>
            )}
            {tab === 'overview' && <OverviewTab detail={detail} />}
            {tab === 'config' && <ConfigTab detail={detail} expandProjects={expandProjects} />}
            {tab === 'activity' && <ActivityPanel pluginId={plugin.id} hasPlatform={plugin.hasPlatform} initialView={activityView} />}
            {detail.versions.length > 1 && <p className="text-[12.5px] text-ink-500">{copy.versionsLine(detail.versions)}</p>}
          </PageBody>
        </Sheet>
      </PageFrame>
      {installer.dialog}
      <ConfirmDialog
        open={removing}
        onOpenChange={setRemoving}
        title={copy.uninstallTitle(localizedText(plugin.displayName))}
        body={copy.uninstallBody}
        confirmLabel={text.uninstall}
        destructive
        onConfirm={async () => {
          await uninstall.mutateAsync({ deleteData })
          void navigate('/plugins')
        }}
      >
        <label className="flex items-start gap-2 text-[13.5px] font-bold [overflow-wrap:anywhere]">
          <input type="checkbox" checked={deleteData} onChange={(event) => setDeleteData(event.target.checked)} className="mt-1 size-4 accent-red" />
          {copy.deleteData(detail.dataDir)}
        </label>
        {detail.providers.length > 0 && <p className="text-[13px] leading-snug text-ink-700">{copy.deleteDataProviders}</p>}
      </ConfirmDialog>
    </DraftNavigationGuard>
  )
}
