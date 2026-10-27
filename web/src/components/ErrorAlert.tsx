import { CircleAlertIcon } from 'lucide-react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { describeError } from '@/lib/errors'

// 请求错误：浅红底、朱红字（alert 的 error 变体），文字 13.5。
export function ErrorAlert({ error, className }: { error: unknown; className?: string }) {
  if (!error) return null
  return (
    <Alert variant="error" className={className}>
      <CircleAlertIcon />
      <AlertDescription className="text-[13.5px] leading-[1.7]">{describeError(error)}</AlertDescription>
    </Alert>
  )
}

// 字段错误：紧跟在输入框下方，13.5 / 700 朱红字。
export function FieldError({ message }: { message?: string }) {
  if (!message) return null
  return <p className="text-[13.5px] leading-snug font-bold text-red-text">{message}</p>
}
