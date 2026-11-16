import { useState, type ReactNode } from 'react'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { ErrorAlert } from './ErrorAlert'

interface ConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  body?: ReactNode
  confirmLabel?: string
  cancelLabel?: string
  destructive?: boolean
  // Whether the dangerous action really cannot be undone (default: yes): pass false for actions that only unlink something, can be added
  // back and lose no data, so the “不可撤销” (Irreversible) stamp is not shown;
  // the “危险操作” (Danger) label, the red shadow and the red confirm button are not affected.
  irreversible?: boolean
  confirmDisabled?: boolean
  scrollBody?: boolean
  // Extra content after the description, e.g. a list of consequences (the description itself is a paragraph of text and cannot hold a list).
  children?: ReactNode
  // When the returned Promise fails, the dialog stays open and shows the error; on success, onOpenChange(false) is called.
  onConfirm: () => Promise<unknown> | void
}

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  body,
  confirmLabel = t.common.confirm,
  cancelLabel = t.common.cancel,
  destructive,
  irreversible = true,
  confirmDisabled = false,
  scrollBody = false,
  onConfirm,
  children,
}: ConfirmDialogProps) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<unknown>(null)
  // Clear the error left from last time on every open, including when the parent component closed the dialog directly.
  const [wasOpen, setWasOpen] = useState(open)
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) setError(null)
  }

  const confirm = async () => {
    if (confirmDisabled || pending) return
    setPending(true)
    setError(null)
    try {
      await onConfirm()
      onOpenChange(false)
    } catch (e) {
      setError(e)
    } finally {
      setPending(false)
    }
  }

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        // It cannot be closed while submitting: both the result and the failure reason have to stay in the dialog.
        if (!next && pending) return
        if (!next) setError(null)
        onOpenChange(next)
      }}
    >
      {/* Confirmation dialog: ZCOOL 28 title with a misregistered pink copy, description 14.5 / 1.75. Delete-type actions (destructive): the
          label “危险操作” (Danger) on the top edge, a red shadow under the sheet, a tilted “不可撤销” (Irreversible) stamp next to the title,
          and a red confirm button. */}
      <AlertDialogContent kind={destructive ? 'danger' : 'confirm'} className="data-[size=default]:sm:max-w-[470px]" paperClassName={`gap-3.5 ${scrollBody ? 'flex flex-col' : ''}`} onEscapeKeyDown={(event) => pending && event.preventDefault()}>
        <AlertDialogHeader className={`gap-3 ${scrollBody ? 'shrink-0' : ''}`}>
          <div className="flex flex-wrap items-center gap-x-3.5 gap-y-2 pr-12">
            <AlertDialogTitle className="leading-[1.2]">{title}</AlertDialogTitle>
            {destructive && irreversible && (
              <span className="riso-tilt inline-flex min-h-6 items-center rounded-[4px] px-2.5 text-[12.5px] font-black tracking-[0.06em] whitespace-nowrap text-red-text [--tilt-bg:transparent] [--tilt-shadow:inset_0_0_0_2px_var(--red-text)] [--tilt:-6deg]">
                {t.common.irreversible}
              </span>
            )}
          </div>
          {body && <AlertDialogDescription className="text-[14.5px] leading-[1.75]">{body}</AlertDialogDescription>}
        </AlertDialogHeader>
        {scrollBody ? <div className="min-h-0 space-y-3 overflow-y-auto overscroll-contain pr-1" data-confirm-scroll>{children}<ErrorAlert error={error} /></div> : <>{children}<ErrorAlert error={error} /></>}
        <AlertDialogFooter className={`mt-2 ${scrollBody ? 'shrink-0' : ''}`}>
          <AlertDialogCancel disabled={pending}>{cancelLabel}</AlertDialogCancel>
          <Button variant={destructive ? 'destructive' : 'default'} disabled={pending || confirmDisabled} onClick={() => void confirm()}>
            {confirmLabel}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
