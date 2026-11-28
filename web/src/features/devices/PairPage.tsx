import { useEffect, useId, useState, type FormEvent } from 'react'
import { Link, useLocation, useNavigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { LoaderCircleIcon, SmartphoneIcon } from 'lucide-react'
import { ApiError, deviceAuth } from '@/api/client'
import { createDeviceSession, extractPairingCode, redeemPairingCode } from '@/api/devices'
import { BrandWordmark } from '@/components/BrandWordmark'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { t } from '@/i18n'
import { describeError, fieldError } from '@/lib/errors'

function pairingErrorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.code === 'pairing_code_invalid') return t.devices.pair.invalidCode
    if (error.code === 'rate_limited') return t.devices.pair.rateLimited
    if (error.code === 'forbidden') return t.devices.pair.localDoorNotice
  }
  return describeError(error)
}

function isLocalDoorRefusal(error: unknown): boolean {
  return error instanceof ApiError && error.code === 'forbidden'
}

// Standalone pairing view used in two places:
// - route mode (/pair#<code>): automatically redeems the one-time code from the URL fragment (clearing the fragment from the address bar
//   immediately) and navigates into the console; falls back to manual entry when opened without a code or when the code fails.
// - gate mode (rendered by AppLayout when the remote door returns 401): replaces the console immediately when an unpaired or removed device
//   opens the remote door, and lets the person pair in place without leaving the URL.
export function PairPage({ mode = 'route' }: { mode?: 'route' | 'gate' }) {
  const copy = t.devices.pair
  const location = useLocation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const [initialCode] = useState(() => (mode === 'route' ? extractPairingCode(location.hash) : ''))
  const [autoPairing, setAutoPairing] = useState(() => initialCode !== '')
  const [codeInput, setCodeInput] = useState('')
  const [nameInput, setNameInput] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [codeError, setCodeError] = useState<string | null>(null)
  const [error, setError] = useState<unknown>(null)

  const codeFieldId = useId()
  const nameFieldId = useId()

  useEffect(() => {
    if (mode !== 'route' || !initialCode) return
    // Remove the one-time secret from the browser address bar and session history as soon as it is read.
    if (typeof window !== 'undefined' && window.location.hash) {
      window.history.replaceState(null, '', `${window.location.pathname}${window.location.search}`)
    }
    let active = true
    void redeemPairingCode(initialCode)
      .then(() => {
        if (!active) return
        queryClient.clear()
        deviceAuth.markAuthenticated()
        void navigate('/', { replace: true })
      })
      .catch((cause) => {
        if (!active) return
        setError(cause)
        setAutoPairing(false)
      })
    return () => {
      active = false
    }
  }, [initialCode, mode, navigate, queryClient])

  const submitManual = async (event: FormEvent) => {
    event.preventDefault()
    if (submitting) return
    const code = extractPairingCode(codeInput)
    if (!code) {
      setCodeError(copy.codeRequired)
      return
    }
    setCodeError(null)
    setError(null)
    setSubmitting(true)
    const trimmedName = nameInput.trim()
    try {
      await createDeviceSession(trimmedName ? { code, name: trimmedName } : { code })
      queryClient.clear()
      deviceAuth.markAuthenticated()
      if (mode === 'route') void navigate('/', { replace: true })
    } catch (cause) {
      setError(cause)
    } finally {
      setSubmitting(false)
    }
  }

  const serverCodeError = fieldError(error, 'code')
  const serverNameError = fieldError(error, 'name')
  const localDoorError = isLocalDoorRefusal(error)

  return (
    <div className="riso-paper flex min-h-dvh w-full flex-col items-center justify-center bg-surface-canvas px-4 py-8 text-ink">
      <div className="w-full max-w-[400px] min-w-0 rounded-panel border border-hairline-strong bg-surface-elevated p-6 shadow-surface">
        <div className="mb-5 flex items-center justify-between gap-3">
          <BrandWordmark />
          <span
            aria-hidden="true"
            className="inline-flex size-8 items-center justify-center rounded-ticket border border-hairline bg-surface-subtle text-ink-700"
          >
            <SmartphoneIcon className="size-4" strokeWidth={1.8} />
          </span>
        </div>

        {autoPairing ? (
          <div role="status" className="flex flex-col gap-3 py-4">
            <div className="flex items-center gap-2.5">
              <LoaderCircleIcon aria-hidden="true" className="size-5 shrink-0 animate-spin text-riso-blue" />
              <h1 className="text-[18px] leading-snug font-semibold tracking-tight">{copy.pairingTitle}</h1>
            </div>
            <p className="text-[13.5px] leading-[1.65] text-ink-700">{copy.pairingBody}</p>
          </div>
        ) : (
          <div className="flex flex-col gap-4">
            <div className="flex flex-col gap-1.5">
              <h1 className="text-[19px] leading-snug font-semibold tracking-tight">{copy.title}</h1>
              <p className="text-[13.5px] leading-[1.65] text-ink-700">
                {mode === 'gate' ? copy.gateBody : copy.manualIntro}
              </p>
            </div>

            {error !== null && (
              <Alert variant={localDoorError ? 'warning' : 'error'} role="alert">
                <AlertDescription className="flex flex-col gap-2.5 text-[13.5px] leading-[1.6]">
                  <span className="break-words">{pairingErrorMessage(error)}</span>
                  {localDoorError && (
                    <div>
                      <Button asChild size="sm" variant="secondary">
                        <Link to="/">{copy.openConsole}</Link>
                      </Button>
                    </div>
                  )}
                </AlertDescription>
              </Alert>
            )}

            <form className="grid gap-3.5" onSubmit={(event) => void submitManual(event)} noValidate>
              <div className="grid gap-1.5">
                <Label htmlFor={codeFieldId} className="text-[13px] font-medium text-ink-700">
                  {copy.codeLabel}
                </Label>
                <Input
                  id={codeFieldId}
                  type="text"
                  autoComplete="off"
                  autoCapitalize="off"
                  autoCorrect="off"
                  spellCheck={false}
                  placeholder={copy.codePlaceholder}
                  value={codeInput}
                  disabled={submitting}
                  aria-invalid={codeError || serverCodeError ? true : undefined}
                  onChange={(event) => {
                    setCodeInput(event.target.value)
                    setCodeError(null)
                  }}
                  className="h-10 font-mono text-[13px]"
                />
                {(codeError || serverCodeError) && (
                  <p role="alert" className="text-[12.5px] font-medium text-red-text">
                    {codeError ?? serverCodeError}
                  </p>
                )}
              </div>

              <div className="grid gap-1.5">
                <Label htmlFor={nameFieldId} className="text-[13px] font-medium text-ink-700">
                  {copy.nameLabel}
                </Label>
                <Input
                  id={nameFieldId}
                  type="text"
                  maxLength={64}
                  placeholder={copy.namePlaceholder}
                  value={nameInput}
                  disabled={submitting}
                  aria-invalid={serverNameError ? true : undefined}
                  onChange={(event) => setNameInput(event.target.value)}
                  className="h-10"
                />
                {serverNameError && (
                  <p role="alert" className="text-[12.5px] font-medium text-red-text">
                    {serverNameError}
                  </p>
                )}
              </div>

              <Button type="submit" size="lg" className="mt-1 w-full" disabled={submitting}>
                {submitting ? copy.submitting : copy.submit}
              </Button>
            </form>
          </div>
        )}
      </div>
    </div>
  )
}
