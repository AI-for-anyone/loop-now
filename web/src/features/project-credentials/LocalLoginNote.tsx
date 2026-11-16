import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { localModeSettingsPath } from '@/features/local-mode/LocalModeCard'
import { t } from '@/i18n'

// 开着"用本机的登录状态"时，插件提供方用的是本机该扩展自己的登录：有就说明不是这里的账号（项目 Agent 是所选的项目账号，
// 平台 Agent 是它自己的登录）；没有就醒目提示启动会失败，并给本机模式设置的入口。
export function LocalLoginNote({ state, owner = 'project' }: { state: NonNullable<Schemas['ModelAuthState']['localLogin']>; owner?: 'project' | 'agent' }) {
  const copy = t.credentials.agentSelection.localLogin
  if (state === 'active') return <p role="note" className="min-w-0 rounded-input bg-tint-blue px-3 py-2 text-[13.5px] [overflow-wrap:anywhere]">{owner === 'agent' ? copy.activeOwn : copy.active}</p>
  return <div role="alert" className="grid min-w-0 gap-1.5 rounded-input bg-tint-red px-3 py-2 text-[13.5px] font-bold text-red-text [overflow-wrap:anywhere]">
    <p>{copy.missing}</p>
    <Link to={localModeSettingsPath} className="justify-self-start text-riso-blue underline outline-none focus-visible:shadow-focus">{copy.settings}</Link>
  </div>
}
