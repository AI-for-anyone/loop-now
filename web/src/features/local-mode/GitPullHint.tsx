import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { useLocalModeSettings } from '@/api/local-mode'
import { t } from '@/i18n'
import { localModeSettingsPath } from './LocalModeCard'

// “从 Git 拉取”失败后、状态栏上方的提示。认证失败或 SSH 主机未确认且“用本机的 git 身份”关着时，提示可以去打开；
// 开着时认证失败照常只看 git 输出，主机未确认则说明要先在本机终端核对指纹。其他失败不提示。
export function GitPullHint({ errorCode }: { errorCode: Schemas['FolderGitPull']['errorCode'] }) {
  const relevant = errorCode === 'auth_failed' || errorCode === 'host_key_unverified'
  const settings = useLocalModeSettings(relevant)
  if (!relevant || !settings.data) return null
  const hints = t.folders.git.hints
  if (settings.data.git && errorCode !== 'host_key_unverified') return null
  return (
    <p role="note" className="shrink-0 border-t border-ink/10 bg-tint-yellow px-4 py-2 text-[12.5px] leading-[1.6] text-ink">
      {settings.data.git ? (
        hints.hostKey
      ) : (
        <>
          {hints.enableGit}{' '}
          <Link to={localModeSettingsPath} className="font-black underline decoration-riso-pink decoration-2 underline-offset-4">
            {hints.openSettings}
          </Link>
        </>
      )}
    </p>
  )
}
