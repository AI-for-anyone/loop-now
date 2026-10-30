import { t } from '@/i18n'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'

export function CredentialConflict({ base, latest, reading, error, onRefresh, onAdopt }: {
  base: string
  latest: { summary: string; deleted?: boolean } | null
  reading: boolean
  error: unknown
  onRefresh: () => void
  onAdopt: () => void
}) {
  return <div role="alert" className="grid min-w-0 gap-3 rounded-card bg-error-field p-4 text-[13.5px]">
    <p className="font-black">{t.credentials.conflict.title}</p>
    <p>{t.credentials.conflict.body}</p>
    <div className="grid min-w-0 gap-3 sm:grid-cols-2">
      <div className="min-w-0 rounded-input bg-white p-3"><p className="font-black">{t.credentials.conflict.whenStarted}</p><p className="whitespace-pre-wrap [overflow-wrap:anywhere]">{base}</p></div>
      <div className="min-w-0 rounded-input bg-white p-3"><p className="font-black">{t.credentials.conflict.latest}</p><p className="whitespace-pre-wrap [overflow-wrap:anywhere]">{latest?.summary ?? (reading ? t.credentials.conflict.reading : t.credentials.conflict.latestMissing)}</p></div>
    </div>
    {latest?.deleted && <p className="font-black">{t.credentials.conflict.accountDeleted}</p>}
    <ErrorAlert error={error} />
    <div className="flex flex-wrap gap-2">
      <Button type="button" size="sm" variant="secondary" disabled={reading} onClick={onRefresh}>{t.credentials.conflict.reread}</Button>
      <Button type="button" size="sm" disabled={reading || !latest || latest.deleted} onClick={onAdopt}>{t.credentials.conflict.adopt}</Button>
    </div>
  </div>
}
