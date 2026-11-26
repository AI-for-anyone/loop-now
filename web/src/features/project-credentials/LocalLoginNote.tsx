import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { localModeSettingsPath } from '@/features/local-mode/LocalModeCard'
import { t } from '@/i18n'

// With "用本机的登录状态" (Use local logins) on, a plugin provider uses that extension's own login on this machine: if there is one, explain
// that it isn't the account here (for a project Agent, the chosen project account;
// for a platform Agent, its own login); if there isn't, warn prominently that starting will fail, and link to the local mode settings.
export function LocalLoginNote({ state, owner = 'project' }: { state: NonNullable<Schemas['ModelAuthState']['localLogin']>; owner?: 'project' | 'agent' }) {
  const copy = t.credentials.agentSelection.localLogin
  if (state === 'active') return <p role="note" className="min-w-0 rounded-input border border-hairline bg-surface-subtle px-3 py-2 text-[13.5px] [overflow-wrap:anywhere] [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue">{owner === 'agent' ? copy.activeOwn : copy.active}</p>
  return <div role="alert" className="grid min-w-0 gap-1.5 rounded-input border border-red/25 bg-tint-red px-3 py-2 text-[13.5px] font-medium text-red-text [overflow-wrap:anywhere] [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:font-bold">
    <p>{copy.missing}</p>
    <Link to={localModeSettingsPath} className="justify-self-start text-ink underline outline-none focus-visible:shadow-focus [[data-skin=plate]_&]:text-riso-blue">{copy.settings}</Link>
  </div>
}
