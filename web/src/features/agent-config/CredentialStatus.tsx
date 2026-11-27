import { LockIcon } from 'lucide-react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'

export function CliToolStatus({ template }: { template: Schemas['CliTemplate'] }) {
  return <div className="min-w-0 text-[13px] [overflow-wrap:anywhere]">
    <p>{t.credentials.status.toolSetupLabel}{t.settings.install[template.install.status]} · {template.name} v{template.version}</p>
    {template.install.error && <p role="status" className="font-bold text-red-text">{template.install.error}</p>}
    <p className="text-ink-700">{t.credentials.status.toolSetupNote}</p>
  </div>
}

// Risk warnings flagged by the server: light-yellow background with a navy top bar.
export function WarningList({ warnings }: { warnings: Schemas['Warning'][] }) {
  if (warnings.length === 0) return null
  return (
    <div className="grid basis-full gap-2">
      {warnings.map((warning) => (
        <Alert key={warning.code} variant="warning" role="note">
          <AlertDescription className="text-[13.5px] text-ink">
            {warning.code === 'host_keychain_risk' ? t.config.keychainRisk : warning.message}
          </AlertDescription>
        </Alert>
      ))}
    </div>
  )
}

// A credential shows only its status and metadata, never the plaintext. Each item is rendered as a flex item on the same row, and
// the outer container decides the layout; the credential's risk warnings take up a full row.
export function CredentialStatus({ credential }: { credential: Schemas['CredentialMeta'] | null }) {
  if (!credential) {
    return (
      <Badge tone="neutral" className="border border-hairline-strong bg-surface-control">
        {t.config.credentialStatus.unset}
      </Badge>
    )
  }
  const set = credential.status === 'set'
  return (
    <>
      <Badge tone={set ? 'success' : 'danger'}>
        {set && <LockIcon />}
        {t.config.credentialStatus[credential.status]}
      </Badge>
      {credential.envNames.length > 0 && <span>{t.config.credentialEnvNames(credential.envNames)}</span>}
      <span className="text-[13px] text-ink-500">{t.common.updatedAt(credential.updatedAt)}</span>
      <WarningList warnings={credential.warnings} />
    </>
  )
}
