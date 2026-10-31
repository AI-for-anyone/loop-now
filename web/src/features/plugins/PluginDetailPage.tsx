import { useState, type ReactNode } from 'react'
import { Link, NavLink, useNavigate, useParams } from 'react-router'
import type { Schemas } from '@/api/client'
import { marketEntryOf, marketSource, usePlugin, usePluginConfig, usePluginMarkets, useProjectPlugin, useSavePluginConfig, useUninstallPlugin } from '@/api/plugins'
import { useProjects } from '@/api/queries'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { SectionError } from '@/components/RequestError'
import { Button } from '@/components/ui/button'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { t } from '@/i18n'
import { isNotFound } from '@/lib/errors'
import { localizedText } from '@/lib/localized-text'
import { pageTitleClass } from '@/lib/page-title'
import { cn } from '@/lib/utils'
import { AccountsManager } from './Accounts'
import { DeliveriesList, LogsView, RecordsTable } from './Activity'
import { ManifestSummary } from './ManifestSummary'
import { cardClass, IdVersion, PanelTitle, Quiet, SourceBadge, StatusLabel } from './parts'
import { pluginPath } from './paths'
import { ProjectEnableList } from './ProjectEnable'
import { ConfigForm } from './SchemaForm'
import { describeValue, fieldsOf } from './schema-form'
import { usePluginInstaller } from './usePluginInstaller'
import { PluginActionButtons } from './PluginActions'

const text = t.plugins

type Detail = Schemas['PluginDetail']
type Tab = 'overview' | 'config' | 'accounts' | 'events' | 'records' | 'logs'
const TABS: Tab[] = ['overview', 'config', 'accounts', 'events', 'records', 'logs']
const copy = text.detail

const tabClass = ({ isActive }: { isActive: boolean }) =>
  cn(
    'inline-flex h-8 items-center rounded-full border-2 px-4 text-[13.5px] font-black whitespace-nowrap outline-none focus-visible:shadow-focus',
    isActive ? 'border-ink bg-riso-yellow text-ink shadow-print-ink' : 'border-transparent text-ink-700 hover:bg-tint-blue hover:text-ink',
  )

function Panel({ title, hint, children, footer, className }: { title: string; hint?: ReactNode; children: ReactNode; footer?: ReactNode; className?: string }) {
  return (
    <section aria-label={title} className={cn(cardClass, 'flex min-w-0 flex-col gap-3 px-4 py-3.5', className)}>
      <PanelTitle title={title} hint={hint} />
      {children}
      {footer}
    </section>
  )
}

const more = (to: string, label: string) => (
  <Link to={to} className="self-start text-[13px] font-bold text-riso-blue underline outline-none focus-visible:shadow-focus">
    {label}
  </Link>
)

function Overview({ detail }: { detail: Detail }) {
  const id = detail.plugin.id
  return (
    <div className="grid min-w-0 gap-3.5 lg:grid-cols-2 lg:items-start">
      <div className="grid min-w-0 gap-3.5">
        <Panel title={copy.contains}>
          <ManifestSummary manifest={detail.manifest} columns={false} />
        </Panel>
        <Panel title={copy.enabledIn} hint={copy.enabledInHint}>
          <ProjectEnableList pluginId={id} schema={detail.manifest.configSchema} />
        </Panel>
      </div>
      <div className="grid min-w-0 gap-3.5">
        <Panel title={copy.recentEvents} footer={more(pluginPath(id, 'events'), copy.allEvents)}>
          <DeliveriesList pluginId={id} compact={4} />
        </Panel>
        <Panel title={copy.records} hint={copy.recordsHint} footer={more(pluginPath(id, 'records'), copy.allRecords)}>
          <RecordsTable pluginId={id} compact={4} />
        </Panel>
        <Panel title={copy.logs} hint={copy.logsHint} footer={more(pluginPath(id, 'logs'), copy.allLogs)}>
          <LogsView pluginId={id} compact={3} />
        </Panel>
      </div>
    </div>
  )
}

// 一个项目对 x-scope: project 字段的覆盖：覆盖了的列出字段与值，没有覆盖的写“用全局值”。
function OverrideRow({ project, detail }: { project: Schemas['Project']; detail: Detail }) {
  const { item } = useProjectPlugin(project.id, detail.plugin.id)
  if (!item?.enabled) return null
  const values = item.config.values as Record<string, unknown>
  const fields = fieldsOf(detail.manifest.configSchema, 'project').filter((field) => values[field.name] !== undefined || item.config.secretsSet.includes(field.name))
  return (
    <li className="grid min-w-0 gap-0.5 rounded-input bg-ink/[0.04] px-3 py-2 text-[13px]">
      <span className="flex items-center gap-2">
        <span className="min-w-0 truncate font-black">{project.name}</span>
        <span className="ml-auto shrink-0 text-ink-700">{fields.length ? text.config.overridden(fields.length) : text.config.useGlobal}</span>
      </span>
      {fields.map((field) => (
        <span key={field.name} className="truncate text-ink-700">
          {field.label} → {field.secret ? t.plugins.form.secretSet : describeValue(field, values[field.name])}
        </span>
      ))}
    </li>
  )
}

function ConfigTab({ detail }: { detail: Detail }) {
  const id = detail.plugin.id
  const config = usePluginConfig(id)
  const save = useSavePluginConfig(id)
  const projects = useProjects()
  const hasProjectFields = fieldsOf(detail.manifest.configSchema, 'project').length > 0
  const scopes = detail.manifest.permissions
  return (
    <div className="grid min-w-0 gap-3.5 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)] lg:items-start">
      <Panel title={text.config.global} hint={text.config.globalHint}>
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
      </Panel>
      <div className="grid min-w-0 gap-3.5">
        {hasProjectFields && (
          <Panel title={text.config.projectOverrides} hint={text.config.projectOverridesHint} footer={<p className="text-[12.5px] text-ink-700">{text.config.editInProject}</p>}>
            <ul className="grid min-w-0 gap-1.5">
              {(projects.data?.projects ?? []).map((project) => (
                <OverrideRow key={project.id} project={project} detail={detail} />
              ))}
            </ul>
          </Panel>
        )}
        <Panel title={text.config.env}>
          <pre className="overflow-x-auto rounded-input bg-ink/[0.04] px-3 py-2.5 font-mono text-[12px] leading-[1.7] whitespace-pre-wrap [overflow-wrap:anywhere]">
            {[`LOOP_NOW_PLUGIN_ROOT=${detail.installDir}`, `LOOP_NOW_PLUGIN_DATA=${detail.dataDir}`, `lnp_… ${scopes.length ? text.config.tokenScopes(text.confirm.list(scopes)) : text.config.noScopes}`].join('\n')}
          </pre>
        </Panel>
      </div>
    </div>
  )
}

// 账号按项目：列出启用了这个插件的项目，每个项目里管理它的账号。
function AccountsTab({ detail }: { detail: Detail }) {
  const projects = useProjects()
  if (!detail.manifest.accounts.length) return <Quiet>{text.accounts.none}</Quiet>
  return (
    <div className="grid min-w-0 gap-3.5">
      {(projects.data?.projects ?? []).map((project) => (
        <ProjectAccounts key={project.id} project={project} detail={detail} />
      ))}
      <p className="text-[12.5px] text-ink-700">{text.accounts.byProject}</p>
    </div>
  )
}

function ProjectAccounts({ project, detail }: { project: Schemas['Project']; detail: Detail }) {
  const { item } = useProjectPlugin(project.id, detail.plugin.id)
  if (!item?.enabled) return null
  return (
    <Panel title={project.name}>
      <AccountsManager projectId={project.id} pluginId={detail.plugin.id} kinds={detail.manifest.accounts} />
    </Panel>
  )
}

// 更新：从市场装的按市场里的版本（有新版本才能点）；其他来源按记录的来源重新取预览，看有没有新版本。
function UpdateButton({ plugin, installer }: { plugin: Schemas['Plugin']; installer: ReturnType<typeof usePluginInstaller> }) {
  const markets = usePluginMarkets()
  const busy = installer.pending !== null
  if (!plugin.source.market)
    return (
      <Button size="sm" disabled={busy} onClick={() => void installer.begin('update', { pluginId: plugin.id })}>
        {busy ? copy.checking : text.update}
      </Button>
    )
  const entry = marketEntryOf(markets.data?.markets, plugin)
  if (!entry?.updateAvailable)
    return (
      <Button size="sm" variant="secondary" disabled>
        {text.markets.upToDate}
      </Button>
    )
  return (
    <Button size="sm" disabled={busy} onClick={() => void installer.begin('update', { source: marketSource(plugin.source.market!, entry) })}>
      {busy ? copy.checking : text.markets.updateTo(entry.version)}
    </Button>
  )
}

// 插件详情：页头（名称、来源、版本、状态，检查更新与卸载）与六个页签，页签写在地址里（/plugins/<id>/<页签>）。
export function PluginDetailPage() {
  const { pluginId = '', tab: tabParam } = useParams()
  const navigate = useNavigate()
  const tab: Tab = TABS.includes(tabParam as Tab) ? (tabParam as Tab) : 'overview'
  const query = usePlugin(pluginId)
  const uninstall = useUninstallPlugin(pluginId)
  const installer = usePluginInstaller()
  const [removing, setRemoving] = useState(false)
  const [deleteData, setDeleteData] = useState(false)
  const detail = query.data

  if (query.isError)
    return (
      <div className="mx-auto flex max-w-[1180px] flex-col gap-3 px-4 pt-[22px] sm:px-8">
        {isNotFound(query.error) ? <p role="status" className="text-[15px] font-bold">{copy.notFound}</p> : <SectionError title={copy.crumb} error={query.error} onRetry={() => void query.refetch()} />}
        {more('/plugins', copy.back)}
      </div>
    )
  if (!detail) return <Quiet>{text.loading}</Quiet>
  const { plugin, manifest } = detail
  const tabs = TABS.filter((value) => value !== 'accounts' || manifest.accounts.length > 0)
  return (
    <DraftNavigationGuard>
      {/* 滚动区是定位容器：里面绝对定位的元素（如 sr-only 说明）随内容滚动，不会按未滚动时的位置把整页撑高。 */}
      <div className="relative h-full overflow-y-auto">
        <div className="mx-auto flex max-w-[1180px] flex-col gap-4 px-4 pt-[22px] pb-16 sm:px-8">
          <header className="flex flex-wrap items-start gap-x-4 gap-y-3">
            <div className="flex min-w-0 flex-[1_1_28rem] flex-col gap-1.5">
              <nav aria-label={t.shell.breadcrumb} className="text-[12px] font-bold text-ink-500">
                {`${text.eyebrow} · `}
                <Link to="/plugins" className="hover:text-ink hover:underline">
                  {copy.crumb}
                </Link>
                {' › '}
                <span aria-current="page" className="text-riso-blue">
                  {plugin.id}
                </span>
              </nav>
              <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
                <h1 className={pageTitleClass}>{localizedText(plugin.displayName)}</h1>
                <SourceBadge source={plugin.source} />
                <IdVersion id={plugin.id} version={plugin.version} />
                <StatusLabel status={plugin.status} />
              </div>
              <p className="text-[13px] text-ink-700 [overflow-wrap:anywhere]">
                {[localizedText(plugin.description), text.from[plugin.source.kind], copy.installedOn(t.chat.time(plugin.installedAt))].filter(Boolean).join(' · ')}
              </p>
            </div>
            <div className="flex flex-wrap items-center gap-2.5 sm:ml-auto">
              <PluginActionButtons plugin={{ id: plugin.id, displayName: localizedText(plugin.displayName) }} actions={manifest.components.actions} page="global" where={{}} />
              <UpdateButton plugin={plugin} installer={installer} />
              <Button size="sm" variant="destructive-link" onClick={() => setRemoving(true)}>
                {text.uninstall}
              </Button>
            </div>
          </header>
          <ErrorAlert error={installer.errorFor('update')} />
          {plugin.status === 'error' && plugin.lastError && (
            <p role="alert" className="rounded-input bg-tint-red px-3.5 py-2.5 text-[13.5px] font-bold text-red-text [overflow-wrap:anywhere]">
              {plugin.lastError}
            </p>
          )}
          <nav aria-label={localizedText(plugin.displayName)} className="-mx-1 flex gap-1.5 overflow-x-auto px-1 pb-1">
            {tabs.map((value) => (
              <NavLink key={value} to={pluginPath(plugin.id, value === 'overview' ? undefined : value)} end className={() => tabClass({ isActive: value === tab })} aria-current={value === tab ? 'page' : undefined}>
                {copy.tabs[value]}
              </NavLink>
            ))}
          </nav>
          {tab === 'overview' && <Overview detail={detail} />}
          {tab === 'config' && <ConfigTab detail={detail} />}
          {tab === 'accounts' && <AccountsTab detail={detail} />}
          {tab === 'events' && (
            <Panel title={copy.tabs.events}>
              <DeliveriesList pluginId={plugin.id} />
            </Panel>
          )}
          {tab === 'records' && (
            <Panel title={copy.records} hint={copy.recordsHint}>
              <RecordsTable pluginId={plugin.id} />
            </Panel>
          )}
          {tab === 'logs' && (
            <Panel title={copy.logs} hint={copy.logsHint}>
              <LogsView pluginId={plugin.id} />
            </Panel>
          )}
          {detail.versions.length > 1 && (
            <p className="text-[12.5px] text-ink-500">
              {copy.versionsLine(detail.versions)}
            </p>
          )}
        </div>
      </div>
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
      </ConfirmDialog>
    </DraftNavigationGuard>
  )
}
