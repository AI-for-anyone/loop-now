import { useSyncExternalStore } from 'react'
import { LockIcon } from 'lucide-react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { ErrorAlert } from '@/components/ErrorAlert'
import { serverVersionStore } from '@/api/client'
import { useCliTemplates, useContextInfo, useHealth } from '@/api/queries'
import { config } from '@/config'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { ContextFolderSection } from '@/features/folders/FolderSections'
import { pageTitleClass } from '@/lib/page-title'

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <>
      <dt className="font-black text-ink-500">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </>
  )
}

// 默认组织、默认项目的信息卡：标签在上，名称用展示字。
function InfoCard({ label, value }: { label: string; value: string }) {
  return (
    <dl className="flex flex-col gap-0.5 rounded-panel bg-paper-raised px-[22px] py-[18px] shadow-print-rest">
      <dt className="text-[13px] font-black text-ink-500">{label}</dt>
      <dd className="font-display-cn text-[30px] leading-[1.25] break-words">{value}</dd>
    </dl>
  )
}

const installTone = (status: string) => (status === 'failed' ? 'danger' : status === 'installed' ? 'success' : 'neutral')

export function SettingsPage() {
  const context = useContextInfo()
  const health = useHealth()
  const templates = useCliTemplates()
  const version = useSyncExternalStore(serverVersionStore.subscribe, serverVersionStore.get)

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-[880px] flex-col gap-[26px] px-4 pt-9 pb-16 sm:px-[30px]">
        <div className="flex flex-wrap items-end gap-x-6 gap-y-3.5">
          <h1 className={pageTitleClass}>{t.settings.title}</h1>
          <Alert variant="note" role="note" className="mb-1.5 w-auto flex-[1_1_320px]">
            <LockIcon />
            <AlertDescription className="text-ink">{t.settings.readonlyHint}</AlertDescription>
          </Alert>
        </div>

        <div className="grid grid-cols-[repeat(auto-fit,minmax(min(260px,100%),1fr))] gap-4">
          <InfoCard label={t.settings.organization} value={context.data?.organization.name ?? '—'} />
          <InfoCard label={t.settings.project} value={context.data?.project.name ?? '—'} />
        </div>

        <ContextFolderSection scope={{ kind: 'global' }} />

        <Card className="gap-4">
          <CardHeader>
            <CardTitle>{t.settings.server}</CardTitle>
          </CardHeader>
          <CardContent>
            <dl className="grid grid-cols-[minmax(110px,140px)_minmax(0,1fr)] gap-x-[18px] gap-y-3.5 text-[14.5px]">
              <Row label={t.settings.apiBase}>
                <span className="font-mono text-[13.5px] break-all">{config.apiBase}</span>
              </Row>
              <Row label={t.settings.version}>
                <span className="font-mono text-[13.5px] break-all">{version ?? '—'}</span>
              </Row>
              <Row label={t.settings.health}>
                {health.isError && <ErrorAlert error={health.error} />}
                {health.data && (
                  <div className="flex flex-col items-start gap-2.5">
                    <Badge tone={health.data.ok ? 'success' : 'warning'}>{health.data.ok ? t.settings.healthOk : t.settings.healthDegraded}</Badge>
                    {(['postgres', 'redis'] as const).map((key) => (
                      <span key={key} className={cn('inline-flex items-center gap-2', !health.data[key].ok && 'font-bold text-red-text')}>
                        <span aria-hidden="true" className={cn('size-[9px] shrink-0 rounded-full', health.data[key].ok ? 'bg-green' : 'bg-red')} />
                        <span>{t.settings.dependency(t.settings[key], health.data[key].ok ? t.settings.healthOk : (health.data[key].error ?? t.errors.unknown))}</span>
                      </span>
                    ))}
                  </div>
                )}
              </Row>
            </dl>
          </CardContent>
        </Card>

        <Card className="gap-4">
          <CardHeader>
            <CardTitle>{t.settings.cliTemplates}</CardTitle>
          </CardHeader>
          <CardContent className="gap-3.5">
            {templates.isError && <ErrorAlert error={templates.error} />}
            {templates.data?.templates.map((template) => (
              <section key={template.name} className="flex flex-col gap-2.5 rounded-card bg-white px-4 py-3.5" aria-label={template.name}>
                <div className="flex flex-wrap items-center gap-x-3.5 gap-y-2">
                  <span className="font-mono text-[15px] font-bold">{template.name}</span>
                  <span className="text-[13px] text-ink-500">{`${t.settings.version} ${template.version}`}</span>
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
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
