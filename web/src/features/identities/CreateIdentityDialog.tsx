import { startTransition, useId, useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { ErrorAlert } from '@/components/ErrorAlert'
import { ApiError } from '@/api/client'
import { useCreateIdentity } from '@/api/inbox'
import { t } from '@/i18n'
import { fieldClass } from '@/lib/field'
import { cn } from '@/lib/utils'
import { identityNameError } from './identity-rules'

// The server's rejections of the name: 409 for a duplicate name, 422 with field name for a rule violation; other errors are
// shown below the form.
function serverNameError(error: unknown): string | undefined {
  if (!(error instanceof ApiError)) return undefined
  if (error.code === 'name_conflict') return t.identities.nameTaken
  return error.fieldMessage('name')
}

// New external identity: the name is the only field, and the address is @name. On success, open its details.
export function CreateIdentityDialog({ onClose }: { onClose: () => void }) {
  const navigate = useNavigate()
  const create = useCreateIdentity()
  const [name, setName] = useState('')
  const [localError, setLocalError] = useState<string | undefined>()
  const hintId = useId()

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    create.reset()
    const rule = identityNameError(name)
    setLocalError(rule)
    if (rule) return
    const identity = await create.mutateAsync(name).catch(() => null)
    if (!identity) return
    void navigate(`/identities/${identity.id}/tokens`)
    startTransition(onClose)
  }

  const serverError = serverNameError(create.error)
  const nameError = localError ?? serverError
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent kind="create" className="sm:max-w-[500px]">
        <DialogHeader>
          <DialogTitle>{t.identities.createTitle}</DialogTitle>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => void submit(e)} noValidate>
          <div className="grid gap-1.5">
            <Label htmlFor="identity-name">{t.identities.name}</Label>
            <div
              className={cn(
                fieldClass,
                'flex items-center px-0 has-focus-visible:border-ink/35 [[data-skin=plate]_&]:has-focus-visible:shadow-[inset_0_0_0_2px_var(--ink),0_0_0_4px_var(--riso-yellow)]',
                nameError && 'border-red bg-error-field [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:shadow-[inset_0_0_0_2px_var(--red)]',
              )}
            >
              <span aria-hidden="true" className="pr-1 pl-3.5 font-mono text-[14px] font-medium text-ink-500 [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-pink-text">
                @
              </span>
              <input
                id="identity-name"
                type="text"
                autoComplete="off"
                spellCheck={false}
                value={name}
                readOnly={create.isPending}
                aria-invalid={nameError ? true : undefined}
                aria-describedby={hintId}
                onChange={(event) => setName(event.target.value)}
                className="h-10 min-w-0 flex-1 border-0 bg-transparent pr-3.5 font-mono text-[14px] text-ink outline-none"
              />
            </div>
            {nameError && (
              <p role="alert" className="text-[12.5px] leading-snug font-bold text-red-text">
                {nameError}
              </p>
            )}
            <p id={hintId} className="text-[12.5px] leading-[1.65] text-ink-500">
              {t.identities.nameHint}
            </p>
          </div>
          {!serverError && <ErrorAlert error={create.error} />}
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={onClose}>
              {t.common.cancel}
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending ? t.identities.creating : t.identities.createSubmit}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
