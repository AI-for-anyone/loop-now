import { useCallback, useEffect, useId, useState } from 'react'
import { Link } from 'react-router'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { desktopCli, type DesktopCliBridge, type DesktopCliStatus } from '@/features/desktop/desktop'
import { t } from '@/i18n'

const statusTone = (state: DesktopCliStatus['state']) =>
  state === 'installed' ? 'success' : state === 'other' ? 'warning' : 'neutral'

// Global settings row for installing loopctl onto the user's terminal PATH.
// In the desktop app it talks to the Electron shell; in a plain browser (server bundle or source install) it shows where loopctl lives
// and one copyable command to put it on PATH.
export function CliToolRow({ version }: { version: string | null }) {
  const bridge = desktopCli()
  if (bridge) return <DesktopCliRow bridge={bridge} />
  return <BrowserCliRow version={version} />
}

function DesktopCliRow({ bridge }: { bridge: DesktopCliBridge }) {
  const c = t.settings.cli
  const [status, setStatus] = useState<DesktopCliStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [installing, setInstalling] = useState(false)
  const [installError, setInstallError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    setLoadError(null)
    try {
      const next = await bridge.status()
      setStatus(next)
    } catch (err) {
      setLoadError(err instanceof Error && err.message ? err.message : c.checkFailed)
    } finally {
      setLoading(false)
    }
  }, [bridge, c.checkFailed])

  useEffect(() => {
    let active = true
    const run = async () => {
      try {
        const next = await bridge.status()
        if (!active) return
        setStatus(next)
        setLoadError(null)
      } catch (err) {
        if (!active) return
        setLoadError(err instanceof Error && err.message ? err.message : c.checkFailed)
      } finally {
        if (active) setLoading(false)
      }
    }
    void run()
    const onFocus = () => void run()
    window.addEventListener('focus', onFocus)
    return () => {
      active = false
      window.removeEventListener('focus', onFocus)
    }
  }, [bridge, c.checkFailed])

  const handleInstall = async () => {
    if (installing) return
    setInstalling(true)
    setInstallError(null)
    try {
      const result = await bridge.install()
      setStatus(result.status)
      if (result.outcome === 'failed') {
        setInstallError(result.error || c.installFailed)
      }
    } catch (err) {
      setInstallError(err instanceof Error && err.message ? err.message : c.installFailed)
    } finally {
      setInstalling(false)
    }
  }

  if (loading && !status && !loadError) {
    return <p className="text-[14px] text-ink-700">{c.checking}</p>
  }

  if (loadError && !status) {
    return (
      <div className="flex flex-col gap-3">
        <p className="text-[14px] text-ink-700">{c.description}</p>
        <Alert variant="error" role="alert">
          <AlertDescription className="text-[13.5px]">{loadError}</AlertDescription>
        </Alert>
        <div>
          <Button type="button" variant="secondary" onClick={() => void refresh()}>
            {c.retry}
          </Button>
        </div>
      </div>
    )
  }

  if (!status) return null

  const canAction = status.state === 'not_installed' || status.state === 'other'
  const actionLabel = installing ? c.installing : status.state === 'other' ? c.replace : c.install

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="flex min-w-0 flex-[1_1_320px] flex-wrap items-center gap-2.5">
          <Badge tone={statusTone(status.state)}>{c.status[status.state]}</Badge>
          <p className="min-w-0 text-[14px] text-ink-700">{c.description}</p>
        </div>
        {canAction && (
          <Button
            type="button"
            variant="secondary"
            disabled={installing || status.moveFirst}
            onClick={() => void handleInstall()}
          >
            {actionLabel}
          </Button>
        )}
      </div>

      {status.state === 'installed' && (
        <>
          <p className="font-mono text-[13px] break-all text-ink-700">
            {status.mode === 'symlink' ? c.installedSymlink(status.location) : c.installedPath(status.location)}
          </p>
          <p className="rounded-card border border-hairline bg-surface-subtle px-3.5 py-2.5 text-[13.5px] leading-relaxed text-ink-700">
            {c.nextStepLead}
            <Link to="/identities" className="font-medium text-ink underline underline-offset-2 hover:text-riso-blue">
              {c.nextStepIdentities}
            </Link>
            {c.nextStepTail}
          </p>
        </>
      )}

      {status.state === 'not_installed' && (
        <p className="text-[13px] text-ink-700">
          {status.mode === 'symlink' ? c.symlinkHint(status.location) : c.pathHint(status.location)}
        </p>
      )}

      {status.state === 'other' && (
        <p className="font-mono text-[13px] break-all text-ink-700">
          {status.points ? c.otherLink(status.location, status.points) : c.otherFile(status.location)}
        </p>
      )}

      {(status.state === 'not_installed' || status.state === 'other') && status.moveFirst && (
        <Alert variant="warning" role="note" className="text-[13.5px]">
          <AlertDescription className="text-[13.5px] text-ink">{c.moveFirst}</AlertDescription>
        </Alert>
      )}

      {status.state === 'unavailable' && status.reason === 'not_packaged' && (
        <p className="text-[13px] text-ink-700">{c.notPackaged}</p>
      )}

      {status.state === 'unavailable' && status.reason === 'error' && (
        <div className="flex flex-col gap-2">
          <Alert variant="error" role="alert">
            <AlertDescription className="text-[13.5px]">{status.error || c.checkFailed}</AlertDescription>
          </Alert>
          <div>
            <Button type="button" variant="secondary" onClick={() => void refresh()}>
              {c.retry}
            </Button>
          </div>
        </div>
      )}

      {installError && (
        <Alert variant="error" role="alert">
          <AlertDescription className="text-[13.5px]">{installError}</AlertDescription>
        </Alert>
      )}
    </div>
  )
}

function BrowserCliRow({ version }: { version: string | null }) {
  const c = t.settings.cli
  const labelId = useId()
  const [copied, setCopied] = useState(false)
  const displayVersion = version ?? '<version>'
  const command = c.browserCommand(displayVersion)

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(command)
    } catch {
      return
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  return (
    <div className="flex flex-col gap-3">
      <p className="text-[14px] text-ink-700">{c.description}</p>
      <p className="text-[13px] text-ink-700">{c.browserWhere(displayVersion)}</p>
      <div className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-1.5">
        <span id={labelId} className="text-[12.5px] leading-4 font-medium tracking-[-0.005em] text-ink-700">
          {c.browserCommandLabel}
        </span>
        <div className="flex items-center gap-2 rounded-input border border-hairline bg-surface-subtle py-1.5 pr-1.5 pl-3">
          <code className="min-w-0 flex-1 overflow-x-auto font-mono text-[12.5px] whitespace-nowrap text-ink select-all">
            {command}
          </code>
          <Button
            type="button"
            size="xs"
            variant="secondary"
            className="h-6.5 px-2.5 text-[12px]"
            aria-describedby={labelId}
            onClick={() => void copy()}
          >
            {copied ? t.common.copied : t.common.copy}
          </Button>
        </div>
      </div>
      <p className="text-[13px] text-ink-700">
        {c.browserNextStepLead}
        <Link to="/identities" className="font-medium text-ink underline underline-offset-2 hover:text-riso-blue">
          {c.nextStepIdentities}
        </Link>
        {c.browserNextStepTail}
      </p>
    </div>
  )
}
