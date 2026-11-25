import { useId, useSyncExternalStore } from 'react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { Sheet } from '@/components/Sheet'
import { SectionToc, SheetSection } from '@/components/SheetSections'
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
import { ModelSettingsCard } from '@/features/settings/ModelSettingsCard'
import { RuntimesCard } from '@/features/settings/RuntimesCard'
import { WritingSettings } from '@/features/writing/WritingSettings'
import { LOCAL_MODE_ANCHOR, LocalModeCard } from '@/features/local-mode/LocalModeCard'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <>
      <dt className="font-black text-ink-500">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </>
  )
}

// The numbered sections of global settings; their order is the order of the section ruler. The local mode section keeps
// LOCAL_MODE_ANCHOR as its id, so links from other pages (#local-mode) keep working.
const KEYS = ['language', 'local-mode', 'runtimes', 'writing', 'models', 'folder', 'server', 'cli-templates'] as const
type Key = (typeof KEYS)[number]
const partId = (key: Key) => (key === 'local-mode' ? LOCAL_MODE_ANCHOR : `settings-${key}`)
const partTitleId = (key: Key) => `${partId(key)}-title`
const titles: Record<Key, string> = {
  language: t.settings.language.title,
  'local-mode': t.localMode.settings.title,
  runtimes: t.runtime.status.title,
  writing: t.settings.writing.title,
  models: t.settings.models.title,
  folder: t.folders.global.title,
  server: t.settings.server,
  'cli-templates': t.settings.cliTemplates,
}

function Part({ id, children }: { id: Key; children: React.ReactNode }) {
  return (
    <SheetSection id={partId(id)} titleId={partTitleId(id)} index={KEYS.indexOf(id)} title={titles[id]}>
      {children}
    </SheetSection>
  )
}

// The UI language is stored only in this browser and has nothing to do with the read-only server config on this page; the page
// reloads after the language changes. Redesign: segmented buttons, with the current language in yellow.
function LanguagePicker() {
  const id = useId()
  return (
    <div className="grid gap-2">
      <ToggleGroup
        aria-labelledby={partTitleId('language')}
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

// Global settings (step 4 of the "printed sheet" redesign): the header is the shared page title (next to it the server version
// and connection state); the eight numbered sections are laid out on one sheet, with the section ruler in the stub on the
// right; what can be changed on this page is written on a yellow sticky note at the top of the sheet.
export function SettingsPage() {
  const context = useContextInfo()
  const health = useHealth()
  const templates = useCliTemplates()
  const version = useSyncExternalStore(serverVersionStore.subscribe, serverVersionStore.get)
  const toc = KEYS.map((key) => ({ id: partId(key), titleId: partTitleId(key), title: titles[key] }))

  return (
    <DraftNavigationGuard>
      <PageFrame>
        <PageHeader crumbs={[{ label: t.shell.global }]} title={t.settings.title}>
          {version && <span className="font-mono text-[12px] text-ink-700">{t.settings.product(version)}</span>}
          {health.data && (
            <span className={cn('inline-flex items-center gap-1.5 text-[12px] font-black', health.data.ok ? 'text-green-text' : 'text-red-text')}>
              <span aria-hidden="true" className={cn('size-[7px] shrink-0 rounded-full', health.data.ok ? 'bg-green' : 'bg-red')} />
              {health.data.ok ? t.settings.serverStatus.ok : t.settings.serverStatus.degraded}
            </span>
          )}
        </PageHeader>
        <Sheet className="mt-2 flex-1">
          <div className="flex min-h-0 flex-1">
            {/* The scroll area is the positioning container: absolutely positioned elements inside it (such as sr-only text)
                scroll with the content instead of stretching the whole page based on their unscrolled position. */}
            <div className="relative h-full min-w-0 flex-1 overflow-y-auto">
              <div className="flex max-w-[960px] flex-col gap-6 pt-[26px] pr-[clamp(14px,3vw,40px)] pb-16 pl-[clamp(12px,2vw,30px)]">
                <StickyNote>{t.settings.readonlyHint}</StickyNote>
                <Part id="language">
                  <LanguagePicker />
                </Part>
                <Part id="local-mode">
                  <LocalModeCard />
                </Part>
                <Part id="runtimes">
                  <RuntimesCard />
                </Part>
                <Part id="writing">
                  <WritingSettings />
                </Part>
                <Part id="models">
                  <ModelSettingsCard />
                </Part>
                <Part id="folder">
                  <ContextFolderBody scope={{ kind: 'global' }} />
                </Part>
                <Part id="server">
                  <dl className="grid grid-cols-[minmax(110px,140px)_minmax(0,1fr)] gap-x-[18px] gap-y-3.5 text-[14.5px]">
                    <Row label={t.settings.organization}>
                      <span className="font-bold [overflow-wrap:anywhere]">{context.data?.organization.name ?? '—'}</span>
                    </Row>
                    <Row label={t.settings.project}>
                      <span className="font-bold [overflow-wrap:anywhere]">{context.data?.project.name ?? '—'}</span>
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
                </Part>
                <Part id="cli-templates">
                  {templates.isError && <ErrorAlert error={templates.error} />}
                  {/* One row per template, with dashed lines between rows (redesign general rule 9). */}
                  <div className="flex flex-col border-t-2 border-ink">
                    {templates.data?.templates.map((template) => (
                      <section key={template.name} className="flex flex-col gap-2.5 border-b-[1.5px] border-dashed border-ink/25 px-1 py-3" aria-label={template.name}>
                        <div className="flex flex-wrap items-center gap-x-3.5 gap-y-2">
                          <span className="font-mono text-[14.5px] font-bold">{template.name}</span>
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
                </Part>
              </div>
            </div>
            <SectionToc label={t.config.toc.title} items={toc} />
          </div>
        </Sheet>
      </PageFrame>
    </DraftNavigationGuard>
  )
}
