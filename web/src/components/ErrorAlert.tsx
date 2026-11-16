import { CircleAlertIcon } from 'lucide-react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { describeError } from '@/lib/errors'

// Request error: vermilion text on light red (the error variant of alert), text size 13.5.
export function ErrorAlert({ error, className }: { error: unknown; className?: string }) {
  if (!error) return null
  return (
    <Alert variant="error" className={className}>
      <CircleAlertIcon />
      <AlertDescription className="text-[13.5px] leading-[1.7]">{describeError(error)}</AlertDescription>
    </Alert>
  )
}

// Field error: right below the input, 13.5 / 700 vermilion text.
export function FieldError({ message }: { message?: string }) {
  if (!message) return null
  return <p className="text-[13.5px] leading-snug font-bold text-red-text">{message}</p>
}
