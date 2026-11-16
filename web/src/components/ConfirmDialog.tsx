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
  // 危险操作是否真的撤销不了（默认是）：只是解除关联、可以重新加回来、不丢数据的操作传 false，不盖“不可撤销”印章；
  // “危险操作”标签、红色投影与红色确认按钮不受影响。
  irreversible?: boolean
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
  irreversible = true,
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
      {/* 确认框：标题 ZCOOL 28 加粉色错版，说明 14.5 / 1.75。删除一类的操作（destructive）：顶边标签“危险操作”、
          纸的投影是红色，标题旁一个“不可撤销”斜印章，确认按钮是红色。 */}
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
