import { useEffect, useId, useRef, useState, type FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import {
  CheckIcon,
  CopyIcon,
  ExternalLinkIcon,
  LoaderCircleIcon,
  LogInIcon,
  LogOutIcon,
  PencilIcon,
  PlusIcon,
  SmartphoneIcon,
  Trash2Icon,
} from 'lucide-react'
import type { Schemas } from '@/api/client'
import {
  deviceKeys,
  useCancelRelaySignIn,
  useDevices,
  useRelayAccount,
  useReleaseTunnelAddress,
  useRemoteAccess,
  useRemoveDevice,
  useRenameDevice,
  useSignOutRelayAccount,
  useStartRelaySignIn,
  useTunnel,
  useUpdateRemoteAccess,
  useUpdateTunnel,
} from '@/api/devices'
import { useContextInfo } from '@/api/queries'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { PageBody } from '@/components/Sheet'
import { SheetSection } from '@/components/SheetSections'
import { StickyNote } from '@/components/StickyNote'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { t } from '@/i18n'
import { copyText } from '@/lib/clipboard'
import { fieldError } from '@/lib/errors'
import { useNow } from '@/lib/use-now'
import { AddDeviceDialog } from './AddDeviceDialog'
import { isAllowedRelaySignInUrl, reserveRelaySignInWindow } from './relayWindow'

const remoteStateTone = (state: Schemas['RemoteAccess']['state']) =>
  state === 'listening' ? 'success' : state === 'failed' ? 'danger' : 'neutral'

const tunnelStateTone = (state: Schemas['Tunnel']['state']) => {
  switch (state) {
    case 'online':
      return 'success'
    case 'failed':
      return 'danger'
    case 'starting':
    case 'downloading':
    case 'connecting':
      return 'busy'
    default:
      return 'neutral'
  }
}

const isTunnelBusy = (state: Schemas['Tunnel']['state']) =>
  state === 'starting' || state === 'downloading' || state === 'connecting'

function parsePort(raw: string): { ok: true; value: number | null } | { ok: false } {
  const trimmed = raw.trim()
  if (trimmed === '') return { ok: true, value: null }
  if (!/^\d+$/.test(trimmed)) return { ok: false }
  const n = Number(trimmed)
  if (!Number.isInteger(n) || n < 1 || n > 65535) return { ok: false }
  return { ok: true, value: n }
}

function RelayAccountSection({ account, loadError }: { account: Schemas['RelayAccount'] | undefined; loadError: unknown }) {
  const copy = t.devices.account
  const startSignIn = useStartRelaySignIn()
  const cancelSignIn = useCancelRelaySignIn()
  const signOut = useSignOutRelayAccount()

  const popupRef = useRef<ReturnType<typeof reserveRelaySignInWindow> | null>(null)
  const [popupBlocked, setPopupBlocked] = useState(false)
  const [copiedSignIn, setCopiedSignIn] = useState(false)
  const signInLinkId = useId()

  useEffect(() => () => popupRef.current?.closeUnused(), [])

  const signIn = account?.signIn ?? null
  const expiresAtMs = signIn ? Date.parse(signIn.expiresAt) : null
  const now = useNow(1000, expiresAtMs !== null)
  const remainingMs = expiresAtMs !== null ? Math.max(0, expiresAtMs - now) : 0
  const signInHref = signIn && isAllowedRelaySignInUrl(signIn.url) ? signIn.url : undefined

  const handleStartSignIn = () => {
    setPopupBlocked(false)
    setCopiedSignIn(false)
    cancelSignIn.reset()
    signOut.reset()
    popupRef.current?.closeUnused()
    const target = reserveRelaySignInWindow()
    popupRef.current = target
    startSignIn.mutate(undefined, {
      onSuccess: (created) => {
        if (!target.navigate(created.url)) setPopupBlocked(true)
      },
      onError: () => target.closeUnused(),
    })
  }

  const handleCancelSignIn = () => {
    popupRef.current?.closeUnused()
    setPopupBlocked(false)
    cancelSignIn.mutate()
  }

  const handleCopySignInLink = async () => {
    if (!signIn) return
    if (await copyText(signIn.url)) {
      setCopiedSignIn(true)
      setTimeout(() => setCopiedSignIn(false), 1500)
    }
  }

  return (
    <SheetSection id="settings-relay-account" titleId="settings-relay-account-title" index={0} title={copy.title}>
      <div className="grid gap-4">
        <p className="text-[13.5px] leading-[1.65] text-ink-700">{copy.intro}</p>
        <ErrorAlert error={loadError} />

        {account?.signedIn && (
          <div className="grid gap-3 border-t border-hairline pt-3.5">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <Badge tone="success">{copy.signedIn}</Badge>
              <Button
                type="button"
                size="sm"
                variant="secondary"
                disabled={signOut.isPending}
                onClick={() => signOut.mutate()}
              >
                <LogOutIcon aria-hidden="true" />
                {signOut.isPending ? copy.signingOut : copy.signOut}
              </Button>
            </div>

            <dl className="grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-x-4 gap-y-2 text-[12.5px]">
              {account.accountId && (
                <div className="flex flex-col gap-0.5">
                  <dt className="text-ink-500">{copy.accountId}</dt>
                  <dd className="font-mono break-all text-ink select-all">{account.accountId}</dd>
                </div>
              )}
              {account.signedInAt && (
                <div className="flex flex-col gap-0.5">
                  <dt className="text-ink-500">{copy.signedInAt}</dt>
                  <dd className="font-mono text-ink-700">{t.common.time(account.signedInAt)}</dd>
                </div>
              )}
              <div className="flex flex-col gap-0.5">
                <dt className="text-ink-500">{copy.relayUrl}</dt>
                <dd className="font-mono break-all text-ink-700">{account.relayUrl}</dd>
              </div>
            </dl>

            <p className="text-[12.5px] leading-[1.6] text-ink-500">{copy.signOutHint}</p>
          </div>
        )}

        {account && !account.signedIn && signIn && (
          <Alert variant="info" role="status" className="min-w-0">
            <AlertTitle className="flex items-center gap-2">
              <LoaderCircleIcon aria-hidden="true" className="size-4 shrink-0 animate-spin text-riso-blue" />
              <span>{copy.waitingTitle}</span>
            </AlertTitle>
            <AlertDescription className="grid min-w-0 gap-3 pt-1">
              <p className="mb-0! text-[13.5px] leading-[1.6] text-ink-700">{copy.waitingBody}</p>
              <p className="mb-0! font-mono text-[12.5px] text-ink-700 tabular-nums">
                {remainingMs > 0 ? copy.expiresIn(remainingMs) : copy.expiredWaiting}
              </p>
              {popupBlocked && <p className="mb-0! text-[13px] font-semibold text-ink">{copy.popupBlocked}</p>}

              <div className="grid gap-1.5">
                <span id={signInLinkId} className="text-[12px] font-medium text-ink-500">
                  {copy.signInLinkLabel}
                </span>
                <div className="flex items-center gap-2 rounded-input border border-code-border bg-code-bg py-1.5 pr-2 pl-2.5">
                  <code
                    aria-labelledby={signInLinkId}
                    className="min-w-0 flex-1 font-mono text-[12px] break-all text-code-fg select-all"
                  >
                    {signIn.url}
                  </code>
                  <Button
                    type="button"
                    size="xs"
                    variant="secondary"
                    className="shrink-0"
                    onClick={() => void handleCopySignInLink()}
                  >
                    {copiedSignIn ? <CheckIcon aria-hidden="true" /> : <CopyIcon aria-hidden="true" />}
                    {copiedSignIn ? t.common.copied : copy.copySignInLink}
                  </Button>
                </div>
              </div>

              <div className="flex flex-wrap gap-2">
                {signInHref && (
                  <Button asChild size="sm" variant="secondary">
                    <a
                      href={signInHref}
                      target="_blank"
                      rel="noopener noreferrer"
                      onClick={() => {
                        popupRef.current?.closeUnused()
                        setPopupBlocked(false)
                      }}
                    >
                      <ExternalLinkIcon aria-hidden="true" />
                      {copy.openSignInPage}
                    </a>
                  </Button>
                )}
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  disabled={cancelSignIn.isPending}
                  onClick={handleCancelSignIn}
                >
                  {cancelSignIn.isPending ? copy.cancellingSignIn : copy.cancelSignIn}
                </Button>
              </div>
            </AlertDescription>
          </Alert>
        )}

        {account && !account.signedIn && !signIn && (
          <div className="grid gap-3">
            <div className="flex flex-wrap items-center gap-3">
              <Button type="button" disabled={startSignIn.isPending} onClick={handleStartSignIn}>
                <LogInIcon aria-hidden="true" />
                {startSignIn.isPending ? copy.startingSignIn : copy.signIn}
              </Button>
              <span className="font-mono text-[12px] text-ink-500">{account.relayUrl}</span>
            </div>

            {account.error && (
              <Alert variant="error" role="alert">
                <AlertTitle>{copy.lastSignInError}</AlertTitle>
                <AlertDescription className="font-mono text-[13px] break-words">{account.error}</AlertDescription>
              </Alert>
            )}
          </div>
        )}

        <ErrorAlert error={startSignIn.error ?? cancelSignIn.error ?? signOut.error} />
      </div>
    </SheetSection>
  )
}

function TunnelSection({
  account,
  tunnel,
  loadError,
  onRequestRelease,
  releasing,
}: {
  account: Schemas['RelayAccount'] | undefined
  tunnel: Schemas['Tunnel'] | undefined
  loadError: unknown
  onRequestRelease: () => void
  releasing: boolean
}) {
  const copy = t.devices.tunnel
  const updateTunnel = useUpdateTunnel()
  const switchId = useId()
  const [copiedUrl, setCopiedUrl] = useState(false)

  const retryAtMs = tunnel?.retryAt ? Date.parse(tunnel.retryAt) : null
  const now = useNow(1000, retryAtMs !== null)
  const retryRemainingMs = retryAtMs !== null ? Math.max(0, retryAtMs - now) : 0

  const signedIn = account?.signedIn === true
  // Even when signed out (e.g. relay returned 401 while the switch stayed on), allow turning the switch off.
  const switchDisabled = updateTunnel.isPending || (!signedIn && !tunnel?.enabled)

  const copyPublicUrl = async () => {
    if (!tunnel?.url) return
    if (await copyText(tunnel.url)) {
      setCopiedUrl(true)
      setTimeout(() => setCopiedUrl(false), 1500)
    }
  }

  const download = tunnel?.state === 'downloading' ? tunnel.download : null
  const downloadPercent =
    download && download.totalBytes && download.totalBytes > 0
      ? Math.min(100, Math.round((download.receivedBytes / download.totalBytes) * 100))
      : null

  return (
    <SheetSection id="settings-tunnel" titleId="settings-tunnel-title" index={1} title={copy.title}>
      <div className="grid gap-4">
        <p className="text-[13.5px] leading-[1.65] text-ink-700">{copy.intro}</p>
        <ErrorAlert error={loadError} />

        {tunnel && (
          <>
            <div className="flex items-center gap-4 border-y border-hairline px-1 py-3">
              <div className="grid min-w-0 flex-1 gap-0.5 leading-[1.5]">
                <label htmlFor={switchId} className="cursor-pointer text-[14.5px] font-semibold">
                  {copy.switchLabel}
                </label>
                <p id={`${switchId}-description`} className="text-[12.5px] text-ink-700">
                  {!signedIn && !tunnel.enabled ? copy.signInFirst : copy.switchDescription}
                </p>
              </div>
              <Switch
                id={switchId}
                checked={tunnel.enabled}
                disabled={switchDisabled}
                aria-describedby={`${switchId}-description`}
                onCheckedChange={(enabled) => updateTunnel.mutate({ enabled })}
              />
              <span aria-hidden="true" className="-ml-2 w-6 text-[12px] font-medium text-ink-700">
                {tunnel.enabled ? copy.on : copy.off}
              </span>
            </div>

            <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
              <Badge tone={tunnelStateTone(tunnel.state)}>
                {isTunnelBusy(tunnel.state) && <LoaderCircleIcon aria-hidden="true" className="animate-spin" />}
                {copy.state[tunnel.state]}
              </Badge>

              {tunnel.url && (
                <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
                  <span className="text-[12.5px] text-ink-500">
                    {tunnel.state === 'online' ? copy.publicAddress : copy.reservedAddress}
                  </span>
                  <code className="font-mono text-[13px] break-all text-ink select-all">{tunnel.url}</code>
                  <Button type="button" size="xs" variant="secondary" onClick={() => void copyPublicUrl()}>
                    {copiedUrl ? <CheckIcon aria-hidden="true" /> : <CopyIcon aria-hidden="true" />}
                    {copiedUrl ? t.common.copied : t.common.copy}
                  </Button>
                </div>
              )}
            </div>

            {download && (
              <div className="grid gap-1.5 rounded-card border border-hairline bg-surface-subtle px-3.5 py-2.5">
                <p className="text-[12.5px] font-medium text-ink-700 tabular-nums">
                  {copy.downloadingDetail(download.version, download.receivedBytes, download.totalBytes)}
                </p>
                <div
                  role="progressbar"
                  aria-label={copy.downloadProgressLabel}
                  aria-valuemin={downloadPercent !== null ? 0 : undefined}
                  aria-valuemax={downloadPercent !== null ? 100 : undefined}
                  aria-valuenow={downloadPercent ?? undefined}
                  className="h-2 w-full overflow-hidden rounded-full bg-hairline"
                >
                  <div
                    className={
                      downloadPercent !== null
                        ? 'h-full rounded-full bg-riso-blue transition-[width] duration-200'
                        : 'h-full w-1/3 animate-pulse rounded-full bg-riso-blue'
                    }
                    style={downloadPercent !== null ? { width: `${downloadPercent}%` } : undefined}
                  />
                </div>
              </div>
            )}

            {tunnel.state === 'failed' && (
              <Alert variant="error" role="alert">
                <AlertDescription className="grid gap-1.5 text-[13px] leading-[1.6]">
                  <p className="mb-0! font-medium">
                    {tunnel.errorCode ? copy.errorCodes[tunnel.errorCode] : copy.errorFallback}
                  </p>
                  {tunnel.error && <p className="mb-0! font-mono text-[12.5px] break-words">{tunnel.error}</p>}
                  {retryAtMs !== null && (
                    <p className="mb-0! font-mono text-[12px] text-ink-700 tabular-nums">
                      {retryRemainingMs > 0 ? copy.retryingIn(retryRemainingMs) : copy.retryingSoon}
                    </p>
                  )}
                </AlertDescription>
              </Alert>
            )}

            {(tunnel.cloudflared || tunnel.hostname) && (
              <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-t border-hairline pt-3 text-[12.5px] text-ink-500">
                <div className="min-w-0">
                  {tunnel.cloudflared && (
                    <span className="font-mono" title={tunnel.cloudflared.path}>
                      {copy.cloudflaredInfo(tunnel.cloudflared.version, copy.cloudflaredSource[tunnel.cloudflared.source])}
                    </span>
                  )}
                </div>
                {tunnel.hostname && (
                  <div className="flex flex-wrap items-center gap-2">
                    {!signedIn && <span className="text-[12px] text-ink-500">{copy.releaseNeedsSignIn}</span>}
                    <Button
                      type="button"
                      size="xs"
                      variant="destructive-link"
                      disabled={!signedIn || releasing}
                      onClick={onRequestRelease}
                    >
                      <Trash2Icon aria-hidden="true" />
                      {copy.release}
                    </Button>
                  </div>
                )}
              </div>
            )}
          </>
        )}

        <ErrorAlert error={updateTunnel.error} />
      </div>
    </SheetSection>
  )
}

function DeviceRow({
  device,
  onRemove,
}: {
  device: Schemas['Device']
  onRemove: (device: Schemas['Device']) => void
}) {
  const copy = t.devices.list
  const rename = useRenameDevice()
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(device.name)
  const [nameError, setNameError] = useState<string | null>(null)
  const inputId = useId()

  const startEdit = () => {
    setDraft(device.name)
    setNameError(null)
    rename.reset()
    setEditing(true)
  }

  const cancelEdit = () => {
    setEditing(false)
    setNameError(null)
    rename.reset()
  }

  const saveEdit = (event: FormEvent) => {
    event.preventDefault()
    const trimmed = draft.trim()
    if (!trimmed) {
      setNameError(copy.nameEmpty)
      return
    }
    if (trimmed === device.name) {
      setEditing(false)
      return
    }
    setNameError(null)
    rename.mutate(
      { deviceId: device.id, name: trimmed },
      {
        onSuccess: () => setEditing(false),
      },
    )
  }

  const serverFieldError = fieldError(rename.error, 'name')

  return (
    <li className="flex flex-col gap-2.5 border-b border-hairline px-1 py-3.5">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <SmartphoneIcon aria-hidden="true" className="size-4 shrink-0 text-ink-500" />
        {editing ? (
          <form className="flex min-w-0 flex-1 flex-wrap items-center gap-2" onSubmit={saveEdit} noValidate>
            <Label htmlFor={inputId} className="sr-only">
              {copy.nameLabel}
            </Label>
            <Input
              id={inputId}
              value={draft}
              maxLength={64}
              autoFocus
              disabled={rename.isPending}
              aria-invalid={nameError || serverFieldError ? true : undefined}
              onChange={(event) => {
                setDraft(event.target.value)
                setNameError(null)
              }}
              onKeyDown={(event) => {
                if (event.key === 'Escape') cancelEdit()
              }}
              className="h-8 min-w-[160px] flex-1"
            />
            <Button type="submit" size="xs" disabled={rename.isPending}>
              {rename.isPending ? t.common.saving : t.common.save}
            </Button>
            <Button type="button" size="xs" variant="secondary" disabled={rename.isPending} onClick={cancelEdit}>
              {t.common.cancel}
            </Button>
          </form>
        ) : (
          <>
            <span className="min-w-0 flex-1 text-[14.5px] font-semibold [overflow-wrap:anywhere]">{device.name}</span>
            <div className="flex shrink-0 items-center gap-1">
              <Button
                type="button"
                variant="ghost"
                size="xs"
                aria-label={copy.renameLabel(device.name)}
                onClick={startEdit}
              >
                <PencilIcon aria-hidden="true" />
                {copy.rename}
              </Button>
              <Button
                type="button"
                variant="destructive-link"
                size="xs"
                aria-label={copy.removeLabel(device.name)}
                onClick={() => onRemove(device)}
              >
                <Trash2Icon aria-hidden="true" />
                {copy.remove}
              </Button>
            </div>
          </>
        )}
      </div>

      {editing && (nameError || serverFieldError) && (
        <p role="alert" className="text-[12.5px] font-medium text-red-text">
          {nameError ?? serverFieldError}
        </p>
      )}
      {editing && !serverFieldError && <ErrorAlert error={rename.error} />}

      <dl className="grid grid-cols-[repeat(auto-fit,minmax(160px,1fr))] gap-x-4 gap-y-1.5 pl-7 text-[12.5px]">
        <div className="flex flex-col gap-0.5">
          <dt className="text-ink-500">{copy.createdAt}</dt>
          <dd className="font-mono text-ink-700">{t.common.time(device.createdAt)}</dd>
        </div>
        <div className="flex flex-col gap-0.5">
          <dt className="text-ink-500">{copy.lastSeenAt}</dt>
          <dd className="font-mono text-ink-700">{t.common.time(device.lastSeenAt)}</dd>
        </div>
        <div className="flex flex-col gap-0.5">
          <dt className="text-ink-500">{copy.lastAddress}</dt>
          <dd className="font-mono break-all text-ink-700">{device.lastAddress ?? copy.unknownAddress}</dd>
        </div>
        <div className="flex flex-col gap-0.5">
          <dt className="text-ink-500">{copy.expiresAt}</dt>
          <dd className="font-mono text-ink-700">{t.common.time(device.expiresAt)}</dd>
        </div>
      </dl>
    </li>
  )
}

// Global settings -> Phones and other devices: manages the Loop Now relay account, Cloudflare tunnel ("Allow access from phones"),
// paired devices with QR pairing, and the underlying local remote-door listener.
// On the remote door itself (Context.door !== 'local'), management endpoints are local-only, so this view shows a plain notice instead
// of calling them.
export function DevicesSettings() {
  const queryClient = useQueryClient()
  const context = useContextInfo()
  const isRemoteDoor = context.data?.door !== undefined && context.data.door !== 'local'
  const canManage = context.data?.door === 'local'

  const relayAccount = useRelayAccount(canManage)
  const tunnelQuery = useTunnel(canManage)
  const releaseTunnel = useReleaseTunnelAddress()
  const remoteAccess = useRemoteAccess(canManage)
  const updateRemote = useUpdateRemoteAccess()
  const devices = useDevices(canManage)
  const removeDevice = useRemoveDevice()

  const switchId = useId()
  const portInputId = useId()
  const [portDraft, setPortDraft] = useState<string | null>(null)
  const [portError, setPortError] = useState<string | null>(null)
  const [copiedUrl, setCopiedUrl] = useState(false)
  const [adding, setAdding] = useState(false)
  const [removing, setRemoving] = useState<Schemas['Device'] | null>(null)
  const [releasingAddress, setReleasingAddress] = useState(false)

  // Keep tunnel and remote-door queries in sync when background sign-in finishes or tunnel state transitions while polling.
  const prevSignedIn = useRef<boolean | undefined>(undefined)
  useEffect(() => {
    const signedIn = relayAccount.data?.signedIn
    if (prevSignedIn.current === false && signedIn === true) {
      void queryClient.invalidateQueries({ queryKey: deviceKeys.tunnel })
      void queryClient.invalidateQueries({ queryKey: deviceKeys.remoteAccess })
    }
    prevSignedIn.current = signedIn
  }, [relayAccount.data?.signedIn, queryClient])

  const prevTunnelKey = useRef<string | null>(null)
  useEffect(() => {
    const tData = tunnelQuery.data
    if (!tData) return
    const nextKey = `${tData.state}:${tData.errorCode ?? ''}`
    const prevKey = prevTunnelKey.current
    prevTunnelKey.current = nextKey
    if (prevKey === null || prevKey === nextKey) return
    if (tData.errorCode === 'signed_out') {
      void queryClient.invalidateQueries({ queryKey: deviceKeys.relayAccount })
    }
    if (tData.state === 'online' || tData.errorCode === 'remote_door_failed') {
      void queryClient.invalidateQueries({ queryKey: deviceKeys.remoteAccess })
    }
  }, [tunnelQuery.data, queryClient])

  if (isRemoteDoor) {
    return (
      <PageBody width="narrow" innerClassName="flex flex-col gap-6 pb-12">
        <SheetSection id="settings-devices" titleId="settings-devices-title" index={0} title={t.devices.title}>
          <StickyNote>{t.devices.localOnly.body}</StickyNote>
        </SheetSection>
      </PageBody>
    )
  }

  const account = relayAccount.data
  const tunnel = tunnelQuery.data
  const remote = remoteAccess.data
  const savedPortText = remote?.port !== null && remote?.port !== undefined ? String(remote.port) : ''
  const portValue = portDraft ?? savedPortText
  const portDirty = portDraft !== null && portDraft.trim() !== savedPortText
  const serverPortError = fieldError(updateRemote.error, 'port')

  const toggleRemote = (enabled: boolean) => {
    if (!remote) return
    setPortError(null)
    updateRemote.mutate({ enabled, port: remote.port })
  }

  const savePort = (event: FormEvent) => {
    event.preventDefault()
    if (!remote) return
    const parsed = parsePort(portValue)
    if (!parsed.ok) {
      setPortError(t.devices.remote.port.invalid)
      return
    }
    setPortError(null)
    updateRemote.mutate(
      { enabled: remote.enabled, port: parsed.value },
      {
        onSuccess: () => setPortDraft(null),
      },
    )
  }

  const copyAddress = async () => {
    if (!remote?.url) return
    if (await copyText(remote.url)) {
      setCopiedUrl(true)
      setTimeout(() => setCopiedUrl(false), 1500)
    }
  }

  const deviceList = devices.data?.devices ?? []
  const canAddDevice = remote?.state === 'listening' || tunnel?.state === 'online'
  const tunnelStarting = tunnel?.enabled === true && tunnel.state !== 'online'

  return (
    <PageBody width="narrow" innerClassName="flex flex-col gap-6 pb-12">
      <RelayAccountSection account={account} loadError={relayAccount.error} />

      <TunnelSection
        account={account}
        tunnel={tunnel}
        loadError={tunnelQuery.error}
        releasing={releaseTunnel.isPending}
        onRequestRelease={() => setReleasingAddress(true)}
      />

      <SheetSection id="settings-devices" titleId="settings-devices-title" index={2} title={t.devices.list.title}>
        <div className="grid gap-4">
          <div className="flex flex-wrap items-start gap-x-4 gap-y-2.5">
            <p className="min-w-0 flex-[1_1_280px] text-[13.5px] leading-[1.65] text-ink-700">{t.devices.list.intro}</p>
            <Button type="button" disabled={!canAddDevice} onClick={() => setAdding(true)}>
              <PlusIcon aria-hidden="true" />
              {t.devices.list.add}
            </Button>
          </div>

          {remote && !canAddDevice && (
            <p className="text-[12.5px] text-ink-500">{t.devices.list.turnOnFirst}</p>
          )}
          {canAddDevice && tunnelStarting && (
            <p className="text-[12.5px] text-ink-500">{t.devices.list.tunnelStartingHint}</p>
          )}

          <ErrorAlert error={devices.error} />

          {devices.isPending && !devices.data && <p className="text-[13.5px] text-ink-500">{t.common.loading}</p>}

          {devices.isSuccess && deviceList.length === 0 && (
            <p className="text-[13.5px] text-ink-500">{t.devices.list.empty}</p>
          )}

          {deviceList.length > 0 && (
            <ul className="flex flex-col border-t border-hairline-strong">
              {deviceList.map((device) => (
                <DeviceRow key={device.id} device={device} onRemove={setRemoving} />
              ))}
            </ul>
          )}
        </div>
      </SheetSection>

      <SheetSection id="settings-remote-access" titleId="settings-remote-access-title" index={3} title={t.devices.remote.title}>
        <div className="grid gap-4">
          <p className="text-[13.5px] leading-[1.65] text-ink-700">{t.devices.remote.intro}</p>
          <ErrorAlert error={remoteAccess.error} />

          {remote && (
            <>
              <div className="flex items-center gap-4 border-y border-hairline px-1 py-3">
                <div className="grid min-w-0 flex-1 gap-0.5 leading-[1.5]">
                  <label htmlFor={switchId} className="cursor-pointer text-[14.5px] font-semibold">
                    {t.devices.remote.switchLabel}
                  </label>
                  <p id={`${switchId}-description`} className="text-[12.5px] text-ink-700">
                    {t.devices.remote.switchDescription}
                  </p>
                </div>
                <Switch
                  id={switchId}
                  checked={remote.enabled}
                  disabled={updateRemote.isPending}
                  aria-describedby={`${switchId}-description`}
                  onCheckedChange={toggleRemote}
                />
                <span aria-hidden="true" className="-ml-2 w-6 text-[12px] font-medium text-ink-700">
                  {remote.enabled ? t.devices.remote.on : t.devices.remote.off}
                </span>
              </div>

              <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
                <Badge tone={remoteStateTone(remote.state)}>{t.devices.remote.state[remote.state]}</Badge>
                {remote.state === 'listening' && remote.url && (
                  <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
                    <span className="text-[12.5px] text-ink-500">{t.devices.remote.address}</span>
                    <code className="font-mono text-[13px] break-all text-ink select-all">{remote.url}</code>
                    <Button type="button" size="xs" variant="secondary" onClick={() => void copyAddress()}>
                      {copiedUrl ? <CheckIcon aria-hidden="true" /> : <CopyIcon aria-hidden="true" />}
                      {copiedUrl ? t.common.copied : t.common.copy}
                    </Button>
                  </div>
                )}
              </div>

              {remote.state === 'failed' && remote.error && (
                <Alert variant="error" role="alert">
                  <AlertDescription className="font-mono text-[13px] break-words">{remote.error}</AlertDescription>
                </Alert>
              )}

              <form className="grid gap-1.5" onSubmit={savePort} noValidate>
                <Label htmlFor={portInputId} className="text-[13px] font-medium text-ink-700">
                  {t.devices.remote.port.label}
                </Label>
                <div className="flex flex-wrap items-center gap-2">
                  <Input
                    id={portInputId}
                    type="text"
                    inputMode="numeric"
                    placeholder={t.devices.remote.port.placeholder}
                    value={portValue}
                    disabled={updateRemote.isPending}
                    aria-invalid={portError || serverPortError ? true : undefined}
                    aria-describedby={`${portInputId}-hint`}
                    onChange={(event) => {
                      setPortDraft(event.target.value)
                      setPortError(null)
                    }}
                    className="w-[150px] font-mono"
                  />
                  <Button type="submit" size="sm" variant="secondary" disabled={!portDirty || updateRemote.isPending}>
                    {updateRemote.isPending ? t.common.saving : t.devices.remote.port.save}
                  </Button>
                </div>
                {(portError || serverPortError) && (
                  <p role="alert" className="text-[12.5px] font-medium text-red-text">
                    {portError ?? serverPortError}
                  </p>
                )}
                <p id={`${portInputId}-hint`} className="text-[12.5px] text-ink-500">
                  {t.devices.remote.port.hint}
                </p>
              </form>
            </>
          )}

          {!serverPortError && <ErrorAlert error={updateRemote.error} />}
        </div>
      </SheetSection>

      {adding && <AddDeviceDialog onClose={() => setAdding(false)} />}

      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => !open && setRemoving(null)}
        title={removing ? t.devices.list.removeTitle(removing.name) : ''}
        body={t.devices.list.removeBody}
        confirmLabel={t.devices.list.remove}
        destructive
        irreversible={false}
        onConfirm={() => (removing ? removeDevice.mutateAsync(removing.id) : undefined)}
      />

      <ConfirmDialog
        open={releasingAddress}
        onOpenChange={setReleasingAddress}
        title={t.devices.tunnel.releaseTitle}
        body={tunnel?.url ? t.devices.tunnel.releaseBody(tunnel.url) : t.devices.tunnel.releaseBodyGeneric}
        confirmLabel={t.devices.tunnel.release}
        destructive
        onConfirm={() => releaseTunnel.mutateAsync()}
      />
    </PageBody>
  )
}
