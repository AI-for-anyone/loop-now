import { useEffect, useId, useState, useSyncExternalStore } from 'react'
import { Link, Navigate, useLocation, useParams } from 'react-router'
import { Button } from '@/components/ui/button'
import { DefaultPermissionMode } from '@/features/permissions/DefaultPermissionMode'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { SubNav, type SubNavLinkItem } from '@/components/IndexTabs'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { PageBody, Sheet } from '@/components/Sheet'
import { SheetSection } from '@/components/SheetSections'
import { StickyNote } from '@/components/StickyNote'
import { ErrorAlert } from '@/components/ErrorAlert'
import { serverVersionStore } from '@/api/client'
import { useCliTemplates, useContextInfo, useHealth } from '@/api/queries'
import { absoluteApiBase } from '@/config'
import { t } from '@/i18n'
import { chooseLocale, locale, localeNames, locales, type Locale } from '@/i18n/locale'
import { segmentedClass, segmentedItemClass } from '@/lib/segmented'
import { cn } from '@/lib/utils'
import { ContextFolderBody } from '@/features/folders/FolderSections'
import { AppearanceCard } from '@/features/settings/AppearanceCard'
import { ModelSettingsCard } from '@/features/settings/ModelSettingsCard'
import { RuntimesCard } from '@/features/settings/RuntimesCard'
import { WritingSettings } from '@/features/writing/WritingSettings'
import { LOCAL_MODE_ANCHOR, LocalModeCard } from '@/features/local-mode/LocalModeCard'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'

type GlobalSettingsTab = 'general' | 'runtimes' | 'workspace' | 'server'
const VALID_TABS = new Set<GlobalSettingsTab>(['general', 'runtimes', 'workspace', 'server'])

// Legacy hash redirects when landing on /settings so old bookmarks and deep links land on the owning tab.
const LEGACY_HASH_REDIRECT: Record<string, string> = {
  [`#${LOCAL_MODE_ANCHOR}`]: `/settings/workspace#${LOCAL_MODE_ANCHOR}`,
  '#settings-folder': '/settings/workspace#settings-folder',
  '#settings-runtimes': '/settings/runtimes#settings-runtimes',
  '#settings-writing': '/settings/runtimes#settings-writing',
  '#settings-models': '/settings/runtimes#settings-models',
  '#settings-server': '/settings/server#settings-server',
  '#settings-clis': '/settings/server#settings-cli-templates',
  '#settings-cli-templates': '/settings/server#settings-cli-templates',
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <>
      <dt className="font-medium text-ink-500 [[data-skin=plate]_&]:font-black">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </>
  )
}

// The UI language is stored only in this browser and has nothing to do with the read-only server config; the page
// reloads after the language changes.
function LanguagePicker({ labelledBy }: { labelledBy: string }) {
  const id = useId()
  return (
    <div className="grid gap-2">
      <ToggleGroup
        aria-labelledby={labelledBy}
        aria-describedby={`${id}-hint`}
        className={cn(segmentedClass, 'h-9 justify-self-start')}
        value={locale}
        onValueChange={(value) => chooseLocale(value as Locale)}
      >
        {locales.map((value) => (
          <ToggleGroupItem key={value} value={value} lang={value} className={segmentedItemClass}>
            {localeNames[value]}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
      <p id={`${id}-hint`} className="text-[13px] text-ink-700">
        {t.settings.language.hint}
      </p>
    </div>
  )
}

const installTone = (status: string) => (status === 'failed' ? 'danger' : status === 'installed' ? 'success' : 'neutral')

// Runtimes & Models tab: inline segmented sub-view switch between Runtimes & Writing (standard scrollable PageBody)
// and models.json (full-width, full-height editor filling the remaining viewport).
function RuntimesAndModelsTab({ hash }: { hash: string }) {
  const hashView: 'runtimes' | 'models' | null =
    hash === '#settings-models' ? 'models' : hash === '#settings-runtimes' || hash === '#settings-writing' ? 'runtimes' : null
  const [override, setOverride] = useState<{ hash: string; view: 'runtimes' | 'models' } | null>(null)
  const view: 'runtimes' | 'models' = override && override.hash === hash ? override.view : (hashView ?? 'runtimes')
  const [everOpenedModels, setEverOpenedModels] = useState(() => view === 'models')
  const modelsMounted = everOpenedModels || view === 'models'

  const switchView = (next: string) => {
    if (next !== 'runtimes' && next !== 'models') return
    if (view === 'models' || next === 'models') setEverOpenedModels(true)
    setOverride({ hash, view: next })
  }

  return (
    <>
      <div className="flex shrink-0 items-center border-b border-hairline px-4 py-2 sm:px-6 [[data-skin=plate]_&]:border-ink/15">
        <ToggleGroup
          aria-label={t.settings.runtimesViews.label}
          className={cn(segmentedClass, 'h-8')}
          value={view}
          onValueChange={switchView}
        >
          <ToggleGroupItem value="runtimes" className={segmentedItemClass}>
            {t.settings.runtimesViews.runtimes}
          </ToggleGroupItem>
          <ToggleGroupItem value="models" className={segmentedItemClass}>
            {t.settings.runtimesViews.models}
          </ToggleGroupItem>
        </ToggleGroup>
      </div>
      {view === 'runtimes' && (
        <PageBody width="default" innerClassName="flex flex-col gap-6 pb-12">
          <SheetSection id="settings-runtimes" titleId="settings-runtimes-title" index={0} title={t.runtime.status.title}>
            <RuntimesCard />
          </SheetSection>
          <SheetSection id="settings-writing" titleId="settings-writing-title" index={1} title={t.settings.writing.title}>
            <WritingSettings />
          </SheetSection>
        </PageBody>
      )}
      {modelsMounted && (
        <div
          hidden={view !== 'models'}
          className={view === 'models' ? 'flex min-h-0 flex-1 flex-col' : 'hidden'}
        >
          <PageBody width="full" innerClassName="flex min-h-0 flex-1 flex-col px-4 py-4 sm:px-6">
            <section id="settings-models" aria-label={t.settings.models.title} className="flex min-h-0 flex-1 flex-col">
              <ModelSettingsCard fullHeight />
            </section>
          </PageBody>
        </div>
      )}
    </>
  )
}

// Workspace & Local Mode tab: inline segmented sub-view switch between Local Mode and full-bleed Global Folder.
function WorkspaceAndLocalModeTab({ hash }: { hash: string }) {
  const hashView: 'local-mode' | 'folder' | null =
    hash === '#settings-folder' ? 'folder' : hash === `#${LOCAL_MODE_ANCHOR}` ? 'local-mode' : null
  const [override, setOverride] = useState<{ hash: string; view: 'local-mode' | 'folder' } | null>(null)
  const view: 'local-mode' | 'folder' = override && override.hash === hash ? override.view : (hashView ?? 'local-mode')

  const switchView = (next: string) => {
    if (next === 'local-mode' || next === 'folder') setOverride({ hash, view: next })
  }

  return (
    <>
      <div className="flex shrink-0 items-center border-b border-hairline px-4 py-2 sm:px-6 [[data-skin=plate]_&]:border-ink/15">
        <ToggleGroup
          aria-label={t.settings.workspaceViews.label}
          className={cn(segmentedClass, 'h-8')}
          value={view}
          onValueChange={switchView}
        >
          <ToggleGroupItem value="local-mode" className={segmentedItemClass}>
            {t.settings.workspaceViews.localMode}
          </ToggleGroupItem>
          <ToggleGroupItem value="folder" className={segmentedItemClass}>
            {t.settings.workspaceViews.folder}
          </ToggleGroupItem>
        </ToggleGroup>
      </div>
      {view === 'local-mode' ? (
        <PageBody width="narrow" innerClassName="flex flex-col gap-6 pb-12">
          <SheetSection id={LOCAL_MODE_ANCHOR} titleId={`${LOCAL_MODE_ANCHOR}-title`} index={0} title={t.localMode.settings.title}>
            <LocalModeCard />
          </SheetSection>
        </PageBody>
      ) : (
        <PageBody width="full" className="overflow-hidden" innerClassName="flex min-h-0 flex-1 flex-col">
          <section
            id="settings-folder"
            aria-label={t.folders.global.title}
            className="flex min-h-0 flex-1 flex-col [&>p]:shrink-0 [&>p]:border-b [&>p]:border-hairline [&>p]:px-4 [&>p]:py-2.5 [&>p]:sm:px-6 [[data-skin=plate]_&>p]:border-ink/15 [&>[data-file-manager]]:!h-auto [&>[data-file-manager]]:min-h-0 [&>[data-file-manager]]:flex-1 [&>[data-file-manager]]:!rounded-none [&>[data-file-manager]]:!border-0"
          >
            <ContextFolderBody scope={{ kind: 'global' }} />
          </section>
        </PageBody>
      )}
    </>
  )
}

// Global settings: 4 URL-backed SubNav tabs replacing the legacy 11-section scroll wall and 196px SectionToc stub.
// DraftNavigationGuard wraps the outer shell so unsaved models.json edits warn before switching tabs or leaving.
export function SettingsPage() {
  const { tab: rawTab } = useParams<{ tab?: string }>()
  const { search, hash } = useLocation()
  const context = useContextInfo()
  const health = useHealth()
  const templates = useCliTemplates()
  const version = useSyncExternalStore(serverVersionStore.subscribe, serverVersionStore.get)

  const activeTab: GlobalSettingsTab =
    rawTab && VALID_TABS.has(rawTab as GlobalSettingsTab) ? (rawTab as GlobalSettingsTab) : 'general'

  useEffect(() => {
    if (!hash) return
    document.getElementById(hash.slice(1))?.scrollIntoView({ block: 'start' })
  }, [activeTab, hash])

  // Canonicalize explicit /settings/general or unknown sub-routes back to /settings.
  if (rawTab && (rawTab === 'general' || !VALID_TABS.has(rawTab as GlobalSettingsTab))) {
    return <Navigate to={`/settings${search}${hash}`} replace />
  }

  // Forward legacy index hashes (#local-mode, #settings-runtimes, #settings-writing, #settings-models, #settings-folder, #settings-server, #settings-clis).
  if (!rawTab && hash && LEGACY_HASH_REDIRECT[hash]) {
    const target = LEGACY_HASH_REDIRECT[hash]
    const [targetPath, targetHash] = target.split('#')
    return <Navigate to={`${targetPath}${search}${targetHash ? `#${targetHash}` : ''}`} replace />
  }

  const tabsCopy = t.settings.tabs
  const navItems: SubNavLinkItem[] = [
    { key: 'general', label: tabsCopy.general, to: '/settings', end: true },
    { key: 'runtimes', label: tabsCopy.runtimes, to: '/settings/runtimes' },
    { key: 'workspace', label: tabsCopy.workspace, to: '/settings/workspace' },
    { key: 'server', label: tabsCopy.server, to: '/settings/server' },
  ]

  return (
    <DraftNavigationGuard>
      <PageFrame>
        <PageHeader crumbs={[{ label: t.shell.global }]} title={t.settings.title}>
          {version && <span className="font-mono text-[12px] text-ink-700">{t.settings.product(version)}</span>}
          {health.data && (
            <span className={cn('inline-flex items-center gap-1.5 text-[12px] font-medium [[data-skin=plate]_&]:font-black', health.data.ok ? 'text-green-text' : 'text-red-text')}>
              <span aria-hidden="true" className={cn('size-[7px] shrink-0 rounded-full', health.data.ok ? 'bg-green' : 'bg-red')} />
              {health.data.ok ? t.settings.serverStatus.ok : t.settings.serverStatus.degraded}
            </span>
          )}
        </PageHeader>
        <SubNav aria-label={tabsCopy.nav} items={navItems} />
        <Sheet className="flex-1">
          {activeTab === 'general' && (
            <PageBody width="narrow" innerClassName="flex flex-col gap-6 pb-12">
              <SheetSection id="settings-language" titleId="settings-language-title" index={0} title={t.settings.language.title}>
                <LanguagePicker labelledBy="settings-language-title" />
              </SheetSection>
              <SheetSection id="settings-appearance" titleId="settings-appearance-title" index={1} title={t.settings.appearance.title}>
                <AppearanceCard labelledBy="settings-appearance-title" />
              </SheetSection>
              <SheetSection id="settings-permissions" titleId="settings-permissions-title" index={2} title={t.permissions.defaultTitle}>
                <DefaultPermissionMode labelledBy="settings-permissions-title" />
              </SheetSection>
              <SheetSection id="settings-import" titleId="settings-import-title" index={3} title={t.importing.entry.settingsTitle}>
                {/* The import page opens by itself only on a fresh install; here it opens any time. */}
                <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
                  <p className="min-w-0 flex-[1_1_320px] text-[14px] text-ink-700">{t.importing.entry.settingsHint}</p>
                  <Button asChild variant="secondary">
                    <Link to="/import">{t.importing.entry.open}</Link>
                  </Button>
                </div>
              </SheetSection>
            </PageBody>
          )}
          {activeTab === 'runtimes' && <RuntimesAndModelsTab hash={hash} />}
          {activeTab === 'workspace' && <WorkspaceAndLocalModeTab hash={hash} />}
          {activeTab === 'server' && (
            <PageBody width="narrow" innerClassName="flex flex-col gap-6 pb-12">
              <StickyNote>{t.settings.readonlyHint}</StickyNote>
              <SheetSection id="settings-server" titleId="settings-server-title" index={0} title={t.settings.server}>
                <dl className="grid grid-cols-[minmax(110px,140px)_minmax(0,1fr)] gap-x-[18px] gap-y-3.5 text-[14.5px]">
                  <Row label={t.settings.organization}>
                    <span className="font-medium [overflow-wrap:anywhere] [[data-skin=plate]_&]:font-bold">{context.data?.organization.name ?? '—'}</span>
                  </Row>
                  <Row label={t.settings.project}>
                    <span className="font-medium [overflow-wrap:anywhere] [[data-skin=plate]_&]:font-bold">{context.data?.project.name ?? '—'}</span>
                  </Row>
                  <Row label={t.settings.apiBase}>
                    <span className="font-mono text-[13.5px] break-all">{absoluteApiBase()}</span>
                  </Row>
                  <Row label={t.settings.version}>
                    <span className="font-mono text-[13.5px] break-all">{version ?? '—'}</span>
                  </Row>
                  <Row label={t.settings.health}>
                    {health.isError && <ErrorAlert error={health.error} />}
                    {health.data && (
                      <div className="flex flex-col items-start gap-2.5">
                        <Badge tone={health.data.ok ? 'success' : 'warning'}>{health.data.ok ? t.settings.healthOk : t.settings.healthDegraded}</Badge>
                        <span className={cn('inline-flex items-center gap-2', !health.data.database.ok && 'font-bold text-red-text')}>
                          <span aria-hidden="true" className={cn('size-[9px] shrink-0 rounded-full', health.data.database.ok ? 'bg-green' : 'bg-red')} />
                          <span>{t.settings.dependency(t.settings.database, health.data.database.ok ? t.settings.healthOk : (health.data.database.error ?? t.errors.unknown))}</span>
                        </span>
                        {/* An oversized WAL is only a warning: it doesn't affect ok, and the sidebar's connection state stays the same. */}
                        {health.data.database.walWarning && (
                          <Alert variant="warning" role="note" className="text-[13.5px]">
                            <AlertDescription className="text-[13.5px] text-ink">{t.settings.walWarning}</AlertDescription>
                          </Alert>
                        )}
                      </div>
                    )}
                  </Row>
                </dl>
              </SheetSection>
              <SheetSection id="settings-cli-templates" titleId="settings-cli-templates-title" index={1} title={t.settings.cliTemplates}>
                {templates.isError && <ErrorAlert error={templates.error} />}
                {/* One row per template: crisp hairline rules in mono skin, solid top + dashed rows in Plate skin. */}
                <div className="flex flex-col border-t border-hairline-strong [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-ink">
                  {templates.data?.templates.map((template) => (
                    <section key={template.name} className="flex flex-col gap-2.5 border-b border-hairline px-1 py-3 [[data-skin=plate]_&]:border-b-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/25" aria-label={template.name}>
                      <div className="flex flex-wrap items-center gap-x-3.5 gap-y-2">
                        <span className="font-mono text-[14px] font-semibold [[data-skin=plate]_&]:text-[14.5px] [[data-skin=plate]_&]:font-bold">{template.name}</span>
                        <span className="text-[12.5px] text-ink-700">
                          {t.settings.version} <span className="font-mono text-ink">{template.version}</span>
                        </span>
                        <Badge className="ml-auto" tone={installTone(template.install.status)}>
                          {t.settings.install[template.install.status]}
                        </Badge>
                      </div>
                      {template.install.error && <p className="font-mono text-[12.5px] break-words text-red-text">{template.install.error}</p>}
                      {template.warnings.map((warning) => (
                        <Alert key={warning.code} variant="warning" role="note" className="text-[13.5px]">
                          <AlertDescription className="text-[13.5px] text-ink">
                            {warning.code === 'host_keychain_risk' ? t.config.keychainRisk : warning.message}
                          </AlertDescription>
                        </Alert>
                      ))}
                    </section>
                  ))}
                </div>
              </SheetSection>
            </PageBody>
          )}
        </Sheet>
      </PageFrame>
    </DraftNavigationGuard>
  )
}
