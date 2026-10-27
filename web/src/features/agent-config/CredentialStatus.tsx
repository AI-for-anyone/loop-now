import { credentialText } from '@/i18n/credential-text'
import { LockIcon } from 'lucide-react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'

export function CliToolStatus({ template }: { template: Schemas['CliTemplate'] }) {
  return <div className="min-w-0 text-[13px] [overflow-wrap:anywhere]">
    <p>{credentialText.credentialStatusText1}{t.settings.install[template.install.status]} · {template.name} v{template.version}</p>
    {template.install.error && <p role="status" className="font-bold text-red-text">{template.install.error}</p>}
    <p className="text-ink-700">{credentialText.credentialStatusText2}</p>
  </div>
}

// 服务端标记的风险提示：浅黄底加藏青顶条。
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

// 凭证只显示状态与元数据，不显示明文。各项作为同一行的弹性条目输出，由外层决定排列；凭证的风险提示占满一行。
export function CredentialStatus({ credential }: { credential: Schemas['CredentialMeta'] | null }) {
  if (!credential) {
    return (
      <Badge tone="neutral" className="bg-white shadow-[inset_0_0_0_2px_rgb(24_38_94/.25)]">
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
