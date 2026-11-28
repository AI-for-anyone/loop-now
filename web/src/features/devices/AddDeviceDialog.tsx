import { useEffect, useId, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { CheckIcon, CopyIcon, RefreshCwIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { deviceKeys, useCreateDevicePairing, useDevicePairingStatus } from '@/api/devices'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { t } from '@/i18n'
import { copyText } from '@/lib/clipboard'
import { isNotFound } from '@/lib/errors'
import { useNow } from '@/lib/use-now'
import { cn } from '@/lib/utils'
import { QrCode } from './QrCode'

function isLocalhostUrl(raw: string): boolean {
  try {
    const host = new URL(raw).hostname
    return host === '127.0.0.1' || host === 'localhost' || host === '::1' || host === '[::1]'
  } catch {
    return false
  }
}

// One-time QR code and pairing link dialog. The secret code and link live only in component state (never in query cache or storage) and
// are cleared on close or as soon as this code is used. While open, GET /device-pairings/{pairingId} tracks this exact code so the paired
// notice always names the device that redeemed it and the used QR disappears immediately.
export function AddDeviceDialog({ onClose }: { onClose: () => void }) {
  const copy = t.devices.addDialog
  const queryClient = useQueryClient()
  const createPairing = useCreateDevicePairing()
  const [pairing, setPairing] = useState<Schemas['DevicePairing'] | null>(null)
  const [paired, setPaired] = useState<{ pairingId: string; device: Schemas['Device'] | null } | null>(null)
  const [copied, setCopied] = useState(false)
  const linkLabelId = useId()
  const startedRef = useRef(false)
  const invalidatedRef = useRef<string | null>(null)

  const pairingStatus = useDevicePairingStatus(pairing?.id ?? null)
  const currentStatus = pairing && pairingStatus.data?.id === pairing.id ? pairingStatus.data : null

  if (pairing && currentStatus?.state === 'used') {
    setPaired({ pairingId: pairing.id, device: currentStatus.device })
    setPairing(null)
    setCopied(false)
  }

  useEffect(() => {
    if (!paired || invalidatedRef.current === paired.pairingId) return
    invalidatedRef.current = paired.pairingId
    void queryClient.invalidateQueries({ queryKey: deviceKeys.devices })
  }, [paired, queryClient])

  const generate = () => {
    setCopied(false)
    createPairing.mutate(undefined, {
      onSuccess: (created) => {
        setPaired(null)
        setPairing(created)
      },
    })
  }

  useEffect(() => {
    if (startedRef.current) return
    startedRef.current = true
    createPairing.mutate(undefined, {
      onSuccess: (created) => {
        setPaired(null)
        setPairing(created)
      },
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const expiresAtMs = pairing ? Date.parse(pairing.expiresAt) : null
  const now = useNow(1000, expiresAtMs !== null)
  const remainingMs = expiresAtMs !== null ? Math.max(0, expiresAtMs - now) : 0
  const expired =
    pairing !== null &&
    (currentStatus?.state === 'expired' || isNotFound(pairingStatus.error) || (expiresAtMs !== null && remainingMs === 0))

  const handleCopy = async () => {
    if (!pairing) return
    const ok = await copyText(pairing.url)
    if (ok) setCopied(true)
  }

  const close = () => {
    setPairing(null)
    setPaired(null)
    onClose()
  }

  return (
    <Dialog open onOpenChange={(open) => !open && close()}>
      <DialogContent kind="create" className="sm:max-w-[500px]">
        <DialogHeader>
          <DialogTitle>{copy.title}</DialogTitle>
          <DialogDescription>{copy.intro}</DialogDescription>
        </DialogHeader>

        {paired && (
          <div className="flex flex-col gap-3 py-2">
            <div
              role="status"
              className="flex items-center gap-2 rounded-card border border-green-text/25 bg-tint-green px-3.5 py-2.5 text-[13.5px] font-medium text-green-text"
            >
              <CheckIcon className="size-4 shrink-0" aria-hidden="true" />
              <span>{paired.device ? copy.pairedNotice(paired.device.name) : copy.pairedGeneric}</span>
            </div>
            <p className="text-[13px] leading-[1.6] text-ink-700">{copy.usedHint}</p>
          </div>
        )}

        <ErrorAlert error={createPairing.error} />

        {createPairing.isPending && !pairing && !paired && (
          <p className="py-8 text-center text-[14px] text-ink-500">{copy.generating}</p>
        )}

        {pairing && (
          <div className="flex flex-col items-center gap-4">
            <div className="relative rounded-card border border-hairline bg-white p-2 shadow-surface">
              <QrCode
                value={pairing.url}
                label={copy.qrLabel}
                className={cn(expired && 'opacity-25')}
              />
              {expired && (
                <div className="absolute inset-0 flex items-center justify-center">
                  <Badge tone="danger">{copy.expired}</Badge>
                </div>
              )}
            </div>

            <div className="flex flex-wrap items-center justify-center gap-2 text-[12.5px] text-ink-700">
              {expired ? (
                <span className="font-medium text-red-text">{copy.expiredHint}</span>
              ) : (
                <span className="font-mono tabular-nums">{copy.validFor(remainingMs)}</span>
              )}
            </div>

            <div className="grid w-full gap-1.5">
              <span id={linkLabelId} className="text-[12.5px] font-medium text-ink-700">
                {copy.linkLabel}
              </span>
              <div className="flex items-center gap-2 rounded-input border border-code-border bg-code-bg py-2 pr-2 pl-3">
                <code
                  aria-labelledby={linkLabelId}
                  className="min-w-0 flex-1 font-mono text-[12.5px] break-all text-code-fg select-all"
                >
                  {pairing.url}
                </code>
                <Button
                  type="button"
                  size="xs"
                  variant="secondary"
                  className="shrink-0"
                  onClick={() => void handleCopy()}
                >
                  {copied ? <CheckIcon aria-hidden="true" /> : <CopyIcon aria-hidden="true" />}
                  {copied ? t.common.copied : copy.copyLink}
                </Button>
              </div>
              {isLocalhostUrl(pairing.url) && (
                <p className="text-[12.5px] leading-[1.6] text-ink-500">{copy.localHint}</p>
              )}
            </div>
          </div>
        )}

        <DialogFooter>
          <Button type="button" variant="secondary" disabled={createPairing.isPending} onClick={generate}>
            <RefreshCwIcon aria-hidden="true" />
            {copy.regenerate}
          </Button>
          <Button type="button" onClick={close}>
            {t.common.close}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
