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
  confirmDisabled?: boolean
  scrollBody?: boolean
  // 说明之后的补充内容，例如逐条列出的后果（说明本身是一段文字，不能放列表）。
  children?: ReactNode
  // 返回的 Promise 失败时，确认框保持打开并显示错误；成功后调用 onOpenChange(false)。
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
  confirmDisabled = false,
  scrollBody = false,
  onConfirm,
  children,
}: ConfirmDialogProps) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<unknown>(null)
  // 每次打开时清除上次留下的错误，包括由父组件直接关闭的情况。
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
        // 提交进行中不能关闭，结果与失败原因都要留在确认框中。
        if (!next && pending) return
        if (!next) setError(null)
        onOpenChange(next)
      }}
    >
      {/* 确认框：标题 ZCOOL 26，说明 14.5 / 1.75；删除类操作的确认按钮用 destructive。 */}
      <AlertDialogContent className={`gap-3 pb-[22px] data-[size=default]:sm:max-w-[470px] ${scrollBody ? 'flex max-h-[calc(100dvh-2rem)] flex-col' : ''}`} onEscapeKeyDown={(event) => pending && event.preventDefault()}>
        <AlertDialogHeader className={`gap-3 ${scrollBody ? 'shrink-0' : ''}`}>
          <AlertDialogTitle className="leading-[1.25]">{title}</AlertDialogTitle>
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
