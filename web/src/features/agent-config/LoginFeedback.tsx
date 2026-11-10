import { ExternalLinkIcon } from 'lucide-react'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'

// 仅是展示状态，正式接口状态由调用方转换；这里不定义 API 数据格式。
type LoginView = 'opening' | 'waiting' | 'success' | 'failed' | 'cancelled' | 'expired'

// title：替换这一状态的标题（例如登录中途等人回答）；openLabel：替换"打开授权页"的文字（例如带上目标主机名）。
export function LoginFeedback({ view, title, hasAccount = false, blocked = false, cancelling = false, detail, authorizationHref, openLabel, retryLabel, onOpen, onCancel, onRetry }: {
  view: LoginView
  title?: string
  openLabel?: string
  hasAccount?: boolean
  blocked?: boolean
  cancelling?: boolean
  detail?: string
  authorizationHref?: string
  retryLabel?: string
  onOpen?: () => void
  onCancel?: () => void
  onRetry?: () => void
}) {
  const waiting = view === 'opening' || view === 'waiting'
  const failed = view === 'failed' || view === 'expired'
  const copy = t.config.modelLogin
  return (
    <Alert variant={failed ? 'error' : 'info'} role={failed ? 'alert' : 'status'} aria-live={failed ? 'assertive' : 'polite'} aria-atomic="true" className="min-w-0">
      <AlertTitle>{title ?? copy.state[view]}</AlertTitle>
      <AlertDescription className="grid min-w-0 gap-2 break-words">
        <p className="mb-0!">{detail ?? copy.description[view]}</p>
        {!waiting && view !== 'success' && hasAccount && <p className="mb-0!">{copy.retained}</p>}
        {blocked && waiting && <p className="mb-0! font-bold">{copy.blocked}</p>}
        <div className="flex flex-wrap gap-2 pt-1">
          {waiting && authorizationHref && <Button asChild size="sm" variant="secondary"><a href={authorizationHref} target="_blank" rel="noopener noreferrer" onClick={onOpen}><ExternalLinkIcon />{openLabel ?? copy.openBrowser}</a></Button>}
          {waiting && !authorizationHref && onOpen && <Button type="button" size="sm" variant="secondary" onClick={onOpen}><ExternalLinkIcon />{copy.openBrowser}</Button>}
          {waiting && onCancel && <Button type="button" size="sm" variant="ghost" disabled={cancelling} onClick={onCancel}>{cancelling ? copy.cancelling : copy.cancel}</Button>}
          {!waiting && view !== 'success' && onRetry && <Button type="button" size="sm" variant="secondary" onClick={onRetry}>{retryLabel ?? copy.retry}</Button>}
        </div>
      </AlertDescription>
    </Alert>
  )
}
