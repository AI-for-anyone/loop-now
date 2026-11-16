import { useState, type ReactNode } from 'react'
import { Link, NavLink, useNavigate, useParams } from 'react-router'
import type { Schemas } from '@/api/client'
import { marketEntryOf, marketSource, usePlugin, usePluginConfig, usePluginDeliveries, usePluginLogs, usePluginMarkets, usePluginReadme, usePluginState, useProjectPlugin, useSavePluginConfig, useUninstallPlugin } from '@/api/plugins'
import { RECORD_PREFIX, recordRows } from './records'
import { useProjects } from '@/api/queries'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { SectionError } from '@/components/RequestError'
import { IndexTabs } from '@/components/IndexTabs'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { Sheet } from '@/components/Sheet'
import { SectionToc, SheetSection } from '@/components/SheetSections'
import { Button } from '@/components/ui/button'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { t } from '@/i18n'
import { isNotFound } from '@/lib/errors'
import { localizedText } from '@/lib/localized-text'
import { indexTabClass } from '@/lib/index-tab'
import { cn } from '@/lib/utils'
import { AccountsManager } from './Accounts'
import { DeliveriesList, LogsView, RecordsTable } from './Activity'
import { ManifestSummary } from './ManifestSummary'
import { cardClass, IdVersion, InstalledSourceBadge, PanelTitle, Quiet, RepositoryLink, StatusLabel } from './parts'
import { pluginPath } from './paths'
import { ProjectEnableList } from './ProjectEnable'
import { ReadmeMarkdown, TruncatedNote } from './Readme'
import { repositoryUrl } from './repository'
import { ConfigForm } from './SchemaForm'
import { describeValue, fieldsOf } from './schema-form'
import { usePluginInstaller } from './usePluginInstaller'
import { PluginActionButtons } from './PluginActions'
import { PluginProviders } from './PluginProviders'

const text = t.plugins

type Detail = Schemas['PluginDetail']
type Tab = 'overview' | 'config' | 'accounts' | 'events' | 'records' | 'logs'
const TABS: Tab[] = ['overview', 'config', 'accounts', 'events', 'records', 'logs']
const copy = text.detail

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

// 有没有模型提供方可说：带 Pi 扩展，或者读出了提供方、出了错。
const hasProviderInfo = (detail: Detail) =>
  detail.manifest.components.agentExtensions > 0 || detail.providers.length > 0 || detail.providerErrors.length > 0 || detail.providersError !== null

// 只算已经读到的条数，后面还有就加“+”（接口不给总数，同文件夹页）。
const loaded = (count: number, more: boolean) => `${count}${more ? '+' : ''}`

// 概览第 04 区“最近动静”：原来分开的事件、投递记录、日志合成三栏，每栏一个大数字、一行说明和“看全部”链接。
function RecentActivity({ pluginId }: { pluginId: string }) {
  const deliveries = usePluginDeliveries(pluginId)
  const records = usePluginState(pluginId, RECORD_PREFIX)
  const logs = usePluginLogs(pluginId)
  const events = (deliveries.data?.pages ?? []).flatMap((page) => page.deliveries)
  const lines = (logs.data?.pages ?? []).flatMap((page) => page.entries)
  const columns = [
    { key: 'events', title: copy.tabs.events, error: deliveries.error, value: deliveries.data && loaded(events.length, deliveries.hasNextPage), note: events.length ? copy.latest(t.chat.time(events[0].at)) : text.events.empty, link: copy.allEvents },
    { key: 'records', title: copy.records, error: records.error, value: records.data && String(recordRows(records.data.entries).length), note: copy.recordsHint, link: copy.allRecords },
    { key: 'logs', title: copy.logs, error: logs.error, value: logs.data && loaded(lines.length, logs.hasNextPage), note: `${copy.logsHint} · ${lines.length ? copy.latest(t.chat.time(lines[0].at)) : text.logs.empty}`, link: copy.allLogs },
  ] as const
  return (
    <div className="-ml-[18px] flex flex-wrap gap-y-3.5">
      {columns.map((column) => (
        <div key={column.key} data-activity={column.key} className="flex min-w-0 flex-[1_1_200px] flex-col gap-2 border-l-2 border-dashed border-ink/30 px-[18px] pt-0.5 pb-1">
          <div className="flex items-baseline justify-between gap-2.5">
            <h3 className="text-[14px] font-black">{column.title}</h3>
            <span className="font-display-latin text-[34px] leading-none font-extrabold text-riso-blue">{column.value || '—'}</span>
          </div>
          {column.error ? <ErrorAlert error={column.error} /> : <p className="text-[12.5px] text-ink-500 [overflow-wrap:anywhere]">{column.note}</p>}
          <Link to={pluginPath(pluginId, column.key)} className="mt-auto self-start text-[13px] font-black text-riso-blue underline underline-offset-3 outline-none focus-visible:shadow-focus">
            {column.link} ›
          </Link>
        </div>
      ))}
    </div>
  )
}

// 概览（改版第 5 步）：按 01–05 编号分区排在纸上——它包含什么、模型提供方（有才显示）、在哪些项目启用、最近动静、
// 说明（有 README 才显示）；右侧票根是目录尺，底部写当前版本与更新状态。
function Overview({ detail, footer }: { detail: Detail; footer: ReactNode }) {
  const id = detail.plugin.id
  const readme = usePluginReadme(id)
  const showReadme = !readme.isPending && readme.data?.file !== null
  const parts: { key: string; title: string; meta?: string; body: ReactNode }[] = [
    { key: 'contains', title: copy.contains, meta: copy.containsHint, body: <ManifestSummary manifest={detail.manifest} columns={false} layout="table" /> },
    ...(hasProviderInfo(detail) ? [{ key: 'providers', title: text.providers.title, meta: text.providers.hint, body: <PluginProviders detail={detail} /> }] : []),
    { key: 'projects', title: copy.enabledIn, meta: copy.enabledInHint, body: <ProjectEnableList pluginId={id} schema={detail.manifest.configSchema} /> },
    { key: 'activity', title: copy.activity, body: <RecentActivity pluginId={id} /> },
    ...(showReadme
      ? [{
          key: 'readme',
          title: t.plugins.readme.title,
          meta: copy.readmeHint,
          body: (
            <div className="flex min-w-0 flex-col gap-2.5 rounded-card border-2 border-ink bg-white px-[22px] py-[18px]">
              {readme.data?.file && <span className="self-start rounded-[4px] bg-tint-blue px-2 font-mono text-[12px] leading-[22px]">{readme.data.file}</span>}
              <ErrorAlert error={readme.error} />
              {readme.data && <ReadmeMarkdown readme={readme.data} />}
              {readme.data?.truncated && <TruncatedNote />}
            </div>
          ),
        }]
      : []),
  ]
  const partId = (key: string) => `plugin-${key}`
  return (
    <div className="flex min-h-0 flex-1">
      {/* 滚动区是定位容器：里面绝对定位的元素（如 sr-only 说明）随内容滚动，不会按未滚动时的位置把整页撑高。 */}
      <div className="relative h-full min-w-0 flex-1 overflow-y-auto">
        <div className="flex max-w-[1000px] flex-col gap-6 pt-6 pr-[clamp(14px,3vw,40px)] pb-16 pl-[clamp(12px,2vw,34px)]">
          {parts.map((part, index) => (
            <SheetSection key={part.key} id={partId(part.key)} titleId={`${partId(part.key)}-title`} index={index} title={part.title} meta={part.meta}>
              {part.body}
            </SheetSection>
          ))}
        </div>
      </div>
      <SectionToc label={t.config.toc.title} items={parts.map((part) => ({ id: partId(part.key), titleId: `${partId(part.key)}-title`, title: part.title }))} footer={footer} />
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

// 插件详情：页头（名称、来源、版本、状态，检查更新与卸载）与六个页签，页签写在地址里（/plugins/<id>/<页签>）。
export function PluginDetailPage() {
  const { pluginId = '', tab: tabParam } = useParams()
  const navigate = useNavigate()
  const tab: Tab = TABS.includes(tabParam as Tab) ? (tabParam as Tab) : 'overview'
  const query = usePlugin(pluginId)
  const uninstall = useUninstallPlugin(pluginId)
  const installer = usePluginInstaller()
  const markets = usePluginMarkets()
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
  // git 来源的仓库：有网页时给链接；SSH 写法没有网页，只显示地址。
  const repository = plugin.source.kind === 'git' ? { location: plugin.source.location, href: repositoryUrl(plugin.source.location, plugin.source.ref) } : null
  const tabs = TABS.filter((value) => value !== 'accounts' || manifest.accounts.length > 0)
  const name = localizedText(plugin.displayName)
  const entry = plugin.source.market ? marketEntryOf(markets.data?.markets, plugin) : undefined
  // 票根底部：当前版本号，下面写更新状态（从市场装的才知道有没有新版本）。
  const versionFooter = (
    <>
      <span className="font-display-latin text-[30px] leading-none font-extrabold text-riso-blue [overflow-wrap:anywhere]">v{plugin.version}</span>
      {entry && <span className="text-[12.5px] font-black text-ink-700">{entry.updateAvailable && entry.version ? copy.updateAvailable(entry.version) : text.markets.upToDate}</span>}
    </>
  )
  return (
    <DraftNavigationGuard>
      {/* 插件详情（改版第 5 步）：紧凑页头（黄色首字母方块、名称、来源与版本、状态，右边是更新状态与红色描边的“卸载”，
          下一行是一句说明和来源）；页签贴在纸张上沿，纸张顶边一条孔版蓝网点色带（同插件市场的卡片）。 */}
      <PageFrame>
        <PageHeader
          crumbs={[{ label: t.shell.global }, { label: copy.crumb, to: '/plugins' }]}
          current={plugin.id}
          title={name}
          lead={
            <span aria-hidden="true" className="flex size-[46px] shrink-0 items-center justify-center self-center rounded-[12px] border-2 border-ink bg-riso-yellow font-display-latin text-[24px] leading-none font-extrabold text-ink shadow-[3px_3px_0_var(--riso-pink)] max-nav:size-10 max-nav:text-[20px]">
              {name.slice(0, 1).toUpperCase()}
            </span>
          }
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
        <ErrorAlert error={installer.errorFor('update')} />
        {plugin.status === 'error' && plugin.lastError && (
          <p role="alert" className="mb-2.5 rounded-input bg-tint-red px-3.5 py-2.5 text-[13.5px] font-bold text-red-text [overflow-wrap:anywhere]">
            {plugin.lastError}
          </p>
        )}
        <IndexTabs aria-label={name}>
          {tabs.map((value) => (
            <NavLink key={value} to={pluginPath(plugin.id, value === 'overview' ? undefined : value)} end className={cn(indexTabClass(value === tab), 'max-sm:px-3')} aria-current={value === tab ? 'page' : undefined}>
              {copy.tabs[value]}
            </NavLink>
          ))}
        </IndexTabs>
        <Sheet className="flex-1">
          <span aria-hidden="true" data-sheet-band className="block h-3.5 shrink-0 border-b-2 border-ink bg-riso-blue bg-[radial-gradient(rgb(255_255_255/.32)_1.2px,transparent_1.5px)] bg-size-[8px_8px]" />
          {tab === 'overview' ? (
            <Overview detail={detail} footer={versionFooter} />
          ) : (
            <div className="relative min-h-0 flex-1 overflow-y-auto">
              <div className="flex max-w-[1180px] flex-col gap-4 pt-6 pr-[clamp(14px,3vw,40px)] pb-16 pl-[clamp(12px,2vw,34px)]">
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
                {detail.versions.length > 1 && <p className="text-[12.5px] text-ink-500">{copy.versionsLine(detail.versions)}</p>}
              </div>
            </div>
          )}
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
