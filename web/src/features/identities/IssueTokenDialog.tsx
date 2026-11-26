import { useId, useState, type FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { CopyIcon } from 'lucide-react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { StickyNote } from '@/components/StickyNote'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioCard, RadioCards } from '@/components/ui/radio-cards'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { Schemas } from '@/api/client'
import { invalidateTokens, issueToken } from '@/api/inbox'
import { config } from '@/config'
import { t } from '@/i18n'
import { defaultExpiry, expiresAtOf, todayInput, type ExpiryChoice } from './identity-rules'

const choices: ExpiryChoice[] = ['never', 'd7', 'd30', 'd90', 'date']

// Issue a token. The plaintext is shown only once, in the issue result: it lives only in this dialog's local state and never
// goes into the query cache, browser storage or the URL; before the result dialog closes, the user must confirm it was saved,
// and it is cleared on close. The expiry defaults to 30 days; “永不过期” (Never expires) can still be chosen.
export function IssueTokenDialog({ identity, onClose }: { identity: Schemas['ExternalIdentity']; onClose: () => void }) {
  const client = useQueryClient()
  const [choice, setChoice] = useState<ExpiryChoice>(defaultExpiry(config.tokenDefaultDays))
  const [date, setDate] = useState('')
  const [dateError, setDateError] = useState<string>()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [issued, setIssued] = useState<Schemas['ExternalTokenIssued'] | null>(null)
  const [confirming, setConfirming] = useState(false)
  const [copied, setCopied] = useState(false)
  const expiryLabelId = useId()
  const secretLabelId = useId()

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const expiresAt = expiresAtOf(choice, date)
    if (expiresAt === undefined) {
      setDateError(t.identities.expiryDateInvalid)
      return
    }
    setDateError(undefined)
    setPending(true)
    setError(null)
    try {
      setIssued(await issueToken(identity.id, expiresAt))
      void invalidateTokens(client, identity.id)
    } catch (cause) {
      setError(cause)
    } finally {
      setPending(false)
    }
  }

  const copy = async () => {
    if (!issued) return
    try {
      await navigator.clipboard.writeText(issued.secret)
    } catch {
      // If the browser doesn't allow writing to the clipboard, leave things as they are; the person can still select and copy by hand.
      return
    }
    setCopied(true)
  }

  const finish = () => {
    setIssued(null)
    onClose()
  }

  if (issued) {
    return (
      <>
        {/* The result dialog can't be closed directly: Esc, clicking the overlay and the close button all ask to confirm it was
            saved first. */}
        <Dialog open onOpenChange={(open) => !open && setConfirming(true)}>
          <DialogContent kind="token" className="sm:max-w-[600px]">
            {/* Redesign step 5: content shown only once gets a yellow sticky-note warning, with the token in a dashed box and a
                yellow “复制” (Copy) button next to it. */}
            <DialogHeader>
              <DialogTitle>{t.identities.issued}</DialogTitle>
            </DialogHeader>
            <StickyNote role="alert" className="items-start text-[13.5px] leading-[1.7] font-normal [&>svg]:mt-[5px]">
              <span className="font-semibold text-red-text [[data-skin=plate]_&]:font-black">{t.identities.onceStrong}</span>
              {t.identities.onceBody}
            </StickyNote>
            <div className="grid gap-1.5">
              <span id={secretLabelId} className="text-[13.5px] font-semibold [[data-skin=plate]_&]:font-black">
                {t.identities.secret}
              </span>
              <div className="flex items-center gap-2.5 rounded-input border border-code-border bg-code-bg py-2 pr-2 pl-4 [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-white">
                <code aria-labelledby={secretLabelId} className="min-w-0 flex-1 font-mono text-[13.5px] break-all text-code-fg select-all [[data-skin=plate]_&]:text-ink">
                  {issued.secret}
                </code>
                <Button type="button" size="xs" variant="secondary" className="mr-0.5 h-8 [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:shadow-[2px_2px_0_var(--ink)]" onClick={() => void copy()}>
                  <CopyIcon aria-hidden="true" />
                  {copied ? t.common.copied : t.common.copy}
                </Button>
              </div>
            </div>
            <dl className="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1 text-[14px]">
              <dt className="font-medium text-ink-500 [[data-skin=plate]_&]:font-black">{t.identities.columns.prefix}</dt>
              <dd className="font-mono">{issued.token.prefix}</dd>
              <dt className="font-medium text-ink-500 [[data-skin=plate]_&]:font-black">{t.identities.columns.expiresAt}</dt>
              <dd>{issued.token.expiresAt ? t.common.time(issued.token.expiresAt) : t.identities.never}</dd>
            </dl>
            <p className="text-[13px] leading-[1.7] text-ink-700">{t.identities.riskShort}</p>
            <DialogFooter>
              <Button type="button" onClick={() => setConfirming(true)}>
                {t.identities.savedClose}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
        <ConfirmDialog
          open={confirming}
          onOpenChange={setConfirming}
          title={t.identities.confirmCloseTitle}
          body={t.identities.confirmCloseBody}
          confirmLabel={t.identities.confirmClose}
          cancelLabel={t.identities.back}
          onConfirm={finish}
        />
      </>
    )
  }

  const preview = choice === 'never' ? null : expiresAtOf(choice, date)
  return (
    <Dialog open onOpenChange={(open) => !open && !pending && onClose()}>
      <DialogContent kind="create" className="sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>{t.identities.issueTitle(identity.name)}</DialogTitle>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => void submit(e)} noValidate>
          <div className="grid gap-2">
            <span id={expiryLabelId} className="text-[13.5px] font-semibold [[data-skin=plate]_&]:font-black">
              {t.identities.expiry}
            </span>
            <RadioCards
              aria-labelledby={expiryLabelId}
              value={choice}
              onValueChange={(value) => {
                setChoice(value as ExpiryChoice)
                setDateError(undefined)
              }}
              className="grid-cols-[repeat(auto-fit,minmax(104px,1fr))] gap-2"
            >
              {choices.map((value) => (
                <RadioCard key={value} value={value} title={t.identities.expiryOptions[value]} className="py-2.5" />
              ))}
            </RadioCards>
            {choice === 'date' && (
              <div className="grid gap-1.5 pt-1">
                <Label htmlFor="token-expiry-date" className="text-[13px] font-medium text-ink-700 [[data-skin=plate]_&]:font-bold">
                  {t.identities.expiryDate}
                </Label>
                <Input
                  id="token-expiry-date"
                  type="date"
                  min={todayInput()}
                  value={date}
                  aria-invalid={dateError ? true : undefined}
                  onChange={(event) => {
                    setDate(event.target.value)
                    setDateError(undefined)
                  }}
                  className="h-[42px] w-[220px]"
                />
                {dateError && (
                  <p role="alert" className="text-[12.5px] font-medium text-red-text [[data-skin=plate]_&]:font-bold">
                    {dateError}
                  </p>
                )}
              </div>
            )}
            <span className="text-[13px] font-medium text-ink-700 [[data-skin=plate]_&]:font-bold">
              {choice === 'never' ? t.identities.neverHint : preview ? t.identities.expiresAt(preview) : null}
            </span>
          </div>
          <Alert variant="warning" role="note">
            <AlertDescription className="text-[13.5px] leading-[1.7] text-ink">{t.identities.riskShort}</AlertDescription>
          </Alert>
          <ErrorAlert error={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" disabled={pending} onClick={onClose}>
              {t.common.cancel}
            </Button>
            <Button type="submit" disabled={pending}>
              {pending ? t.identities.issuing : t.identities.issueSubmit}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
