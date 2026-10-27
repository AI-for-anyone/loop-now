import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { useResetSession } from '@/api/queries'
import { t } from '@/i18n'

// 会话文件丢失的横幅：浅红网点底加藏青印刷投影。仍有执行未结束时不能重开会话。
export function SessionMissingBanner({ agentId, hasRunInProgress }: { agentId: string; hasRunInProgress: boolean }) {
  const reset = useResetSession(agentId)
  const [confirming, setConfirming] = useState(false)
  return (
    <div className="shrink-0 pt-5">
      <div
        role="alert"
        className="riso-halftone-red flex max-w-[760px] flex-wrap items-center gap-x-5 gap-y-3.5 rounded-panel bg-tint-red px-[22px] py-[18px] shadow-[4px_4px_0_var(--ink)]"
      >
        <div className="flex min-w-0 flex-[1_1_420px] flex-col gap-1">
          <p className="font-display-cn text-[28px] leading-[1.2] text-red-text">{t.chat.sessionMissing.title}</p>
          <p className="text-[14px] leading-[1.75]">{t.chat.sessionMissing.body}</p>
          {hasRunInProgress && <p className="text-[13.5px] font-black text-red-text">{t.chat.sessionMissing.busy}</p>}
        </div>
        <Button variant="destructive" size="lg" disabled={hasRunInProgress} onClick={() => setConfirming(true)}>
          {t.chat.sessionMissing.action}
        </Button>
      </div>
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t.chat.sessionMissing.confirmTitle}
        body={t.chat.sessionMissing.confirmBody}
        confirmLabel={t.chat.sessionMissing.action}
        destructive
        onConfirm={() => reset.mutateAsync()}
      />
    </div>
  )
}
