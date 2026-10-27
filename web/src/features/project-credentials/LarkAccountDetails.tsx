import type { ReactNode } from 'react'
import type { Schemas } from '@/api/client'
import { Badge } from '@/components/ui/badge'
import { larkText as copy } from '@/i18n/lark'
import { accountStatus } from './account-copy'

// Presentation only. Identity/grants are supplied by the eventual generated
// contract adapter; never infer them from a remark or requested scopes.
export function LarkAccountDetails({ account, identity, grantedScopes, showName = true }: {
  account: Schemas['ProjectCredentialAccount']; identity?: ReactNode; grantedScopes?: readonly string[] | null; showName?: boolean
}) {
  const user = account.authMethod === 'oauth'
  const verified = user ? account.larkIdentity : null
  const scopes = grantedScopes === undefined ? verified?.grantedScopes : grantedScopes
  const application = verified ?? account.larkApplication
  return <section aria-label={copy.account} className="grid min-w-0 gap-2 text-sm [overflow-wrap:anywhere]">
    <div className="flex flex-wrap gap-2">{showName && <strong>{account.name}</strong>}<Badge>{user ? copy.user : copy.bot}</Badge>{showName && <span>{accountStatus[account.status]}</span>}</div>
    {application && <p>{copy.appSummary(application.brand, application.appId)}</p>}
    {user && <dl className="grid gap-2"><div><dt className="font-bold">{copy.identity}</dt><dd>{identity ?? verified?.displayName ?? copy.unknownIdentity}</dd></div>{verified && <><div><dt className="font-bold">{copy.openId}</dt><dd>{verified.openId}</dd></div><div><dt className="font-bold">{copy.verifiedAt}</dt><dd>{verified.verifiedAt}</dd></div></>}<div><dt className="font-bold">{copy.grants}</dt><dd>{scopes == null ? copy.unknownGrants : scopes.length ? scopes.join(' · ') : copy.emptyGrants}</dd></div></dl>}
    <p>{copy.resource}</p>
  </section>
}
