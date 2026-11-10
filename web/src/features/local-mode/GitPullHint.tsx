import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { useLocalModeSettings } from '@/api/local-mode'
import { t } from '@/i18n'
import { localModeSettingsPath } from './LocalModeCard'

const isHttp = (url: string) => /^https?:\/\//i.test(url.trim())

// “从 Git 拉取”失败后、状态栏上方的提示。
// - SSH 主机未确认：开关开不开都一样（ssh 按系统账户的主目录找 ~/.ssh），统一提示先在本机终端核对指纹。
// - HTTPS 地址认证失败且“用本机的 git 身份”关着：提示去打开，开启后才能用本机已保存的 git 凭证。
// 其他情况（含 SSH 地址认证失败）不加提示，照常看 git 输出；只有 HTTPS 认证失败时才读取开关。
export function GitPullHint({ pull }: { pull: Pick<Schemas['FolderGitPull'], 'errorCode' | 'url'> }) {
  const httpAuth = pull.errorCode === 'auth_failed' && isHttp(pull.url)
  const settings = useLocalModeSettings(httpAuth)
  const hints = t.folders.git.hints
  const hostKey = pull.errorCode === 'host_key_unverified'
  const enableGit = httpAuth && settings.data?.git === false
  if (!hostKey && !enableGit) return null
  return (
    <p role="note" className="shrink-0 border-t border-ink/10 bg-tint-yellow px-4 py-2 text-[12.5px] leading-[1.6] text-ink">
      {hostKey ? (
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
