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

// 签发令牌。明文只在签发结果中显示一次：只存在这个对话框的局部状态里，不进入查询缓存、浏览器存储或地址；
// 结果框关闭前要确认已保存，关闭时清空。有效期默认预选 30 天，“永不过期”仍然可以选。
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
      // 浏览器不允许写剪贴板时保持原样，人仍可以手动选中复制。
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
        {/* 结果框不能直接关掉：Esc、点遮罩与关闭按钮都先确认已保存。 */}
        <Dialog open onOpenChange={(open) => !open && setConfirming(true)}>
          <DialogContent kind="token" className="sm:max-w-[600px]">
            {/* 改版第 5 步：只显示一次的内容——黄色便签提醒，令牌放在虚线框里，旁边黄色“复制”按钮。 */}
            <DialogHeader>
              <DialogTitle>{t.identities.issued}</DialogTitle>
            </DialogHeader>
            <StickyNote role="alert" className="items-start text-[13.5px] leading-[1.7] font-normal [&>svg]:mt-[5px]">
              <span className="font-black text-red-text">{t.identities.onceStrong}</span>
              {t.identities.onceBody}
            </StickyNote>
            <div className="grid gap-1.5">
              <span id={secretLabelId} className="text-[13.5px] font-black">
                {t.identities.secret}
              </span>
              <div className="flex items-center gap-2.5 rounded-input border-2 border-dashed border-ink bg-white py-2 pr-2 pl-4">
                <code aria-labelledby={secretLabelId} className="min-w-0 flex-1 font-mono text-[13.5px] break-all text-ink select-all">
                  {issued.secret}
                </code>
                <Button type="button" size="xs" className="mr-0.5 h-8 border-2 border-ink bg-riso-yellow text-ink shadow-[2px_2px_0_var(--ink)]" onClick={() => void copy()}>
                  <CopyIcon aria-hidden="true" />
                  {copied ? t.common.copied : t.common.copy}
                </Button>
              </div>
            </div>
            <dl className="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1 text-[14px]">
              <dt className="font-black text-ink-500">{t.identities.columns.prefix}</dt>
              <dd className="font-mono">{issued.token.prefix}</dd>
              <dt className="font-black text-ink-500">{t.identities.columns.expiresAt}</dt>
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
            <span id={expiryLabelId} className="text-[13.5px] font-black">
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
                <Label htmlFor="token-expiry-date" className="text-[13px] font-bold text-ink-700">
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
                  <p role="alert" className="text-[12.5px] font-bold text-red-text">
                    {dateError}
                  </p>
                )}
              </div>
            )}
            <span className="text-[13px] font-bold text-ink-700">
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
