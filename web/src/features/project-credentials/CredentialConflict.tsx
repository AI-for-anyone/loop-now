import { credentialText } from '@/i18n/credential-text'
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
    <p className="font-black">{credentialText.credentialConflictText1}</p>
    <p>{credentialText.credentialConflictText2}</p>
    <div className="grid min-w-0 gap-3 sm:grid-cols-2">
      <div className="min-w-0 rounded-input bg-white p-3"><p className="font-black">{credentialText.credentialConflictText3}</p><p className="whitespace-pre-wrap [overflow-wrap:anywhere]">{base}</p></div>
      <div className="min-w-0 rounded-input bg-white p-3"><p className="font-black">{credentialText.credentialConflictText4}</p><p className="whitespace-pre-wrap [overflow-wrap:anywhere]">{latest?.summary ?? (reading ? credentialText.credentialConflictText5 : credentialText.credentialConflictText6)}</p></div>
    </div>
    {latest?.deleted && <p className="font-black">{credentialText.credentialConflictText7}</p>}
    <ErrorAlert error={error} />
    <div className="flex flex-wrap gap-2">
      <Button type="button" size="sm" variant="secondary" disabled={reading} onClick={onRefresh}>{credentialText.credentialConflictText8}</Button>
      <Button type="button" size="sm" disabled={reading || !latest || latest.deleted} onClick={onAdopt}>{credentialText.credentialConflictText9}</Button>
    </div>
  </div>
}
