import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { useLocalModeSettings } from '@/api/local-mode'
import { t } from '@/i18n'
import { localModeSettingsPath } from './LocalModeCard'

const isHttp = (url: string) => /^https?:\/\//i.test(url.trim())

// The hint shown above the status bar after “从 Git 拉取” (Pull from Git) fails.
// - SSH host not verified: the switch makes no difference either way (ssh looks for ~/.ssh in the system account's home
//   directory), so always suggest checking the fingerprint in a terminal on this computer first.
// - Authentication failed for an HTTPS address and “用本机的 git 身份” (Use local git identity) is off: suggest turning it on,
//   since only then can the git credentials saved on this computer be used.
// In all other cases (including authentication failures for SSH addresses) no hint is added and the git output is read as
// usual; the switch is only read when HTTPS authentication failed.
export function GitPullHint({ pull }: { pull: Pick<Schemas['FolderGitPull'], 'errorCode' | 'url'> }) {
  const httpAuth = pull.errorCode === 'auth_failed' && isHttp(pull.url)
  const settings = useLocalModeSettings(httpAuth)
  const hints = t.folders.git.hints
  const hostKey = pull.errorCode === 'host_key_unverified'
  const enableGit = httpAuth && settings.data?.git === false
  if (!hostKey && !enableGit) return null
  return (
    <p role="note" className="shrink-0 border-t border-hairline bg-tint-yellow px-3 py-1.5 text-[12px] leading-[1.5] text-ink [[data-skin=plate]_&]:border-ink/10">
      {hostKey ? (
        hints.hostKey
      ) : (
        <>
          {hints.enableGit}{' '}
          <Link to={localModeSettingsPath} className="font-medium underline decoration-ink decoration-1 underline-offset-4 hover:text-ink-700 [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:decoration-riso-pink [[data-skin=plate]_&]:decoration-2">
            {hints.openSettings}
          </Link>
        </>
      )}
    </p>
  )
}
