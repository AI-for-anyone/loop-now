import { useState } from 'react'
import { useNavigate } from 'react-router'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import type { Schemas } from '@/api/client'
import { useDeleteAgent } from '@/api/queries'
import { useScope } from '@/features/projects/scope-context'
import { t } from '@/i18n'
import { agentsPath } from '@/lib/scope'
import { Section } from './Section'
import { skipUnsavedPrompt } from './unsaved-guard'

export function DangerZone({ agent, idle }: { agent: Schemas['Agent']; idle: boolean }) {
  const navigate = useNavigate()
  const scope = useScope()
  const remove = useDeleteAgent(agent.id)
  const [confirming, setConfirming] = useState(false)
  const body = agent.scope === 'project' ? t.config.danger.projectConfirmBody : t.config.danger.confirmBody
  return (
    <Section id="danger" title={t.config.danger.title} tone="danger">
      <p className="text-[14.5px]">{body}</p>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2.5">
        <Button variant="destructive" disabled={!idle} onClick={() => setConfirming(true)}>
          {t.config.danger.action}
        </Button>
        {!idle && <p className="text-[13.5px] font-bold text-red-text">{t.config.danger.notIdle}</p>}
      </div>
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t.config.danger.confirmTitle(agent.name)}
        body={
          agent.isManager ? (
            <>
              <span className="block">{body}</span>
              {/* 担任项目 Manager 时多一块浅蓝底、加粗的说明。 */}
              <span className="mt-2 block rounded-input bg-tint-blue px-3 py-2 font-black text-ink">{t.config.danger.managerNote}</span>
            </>
          ) : (
            body
          )
        }
        confirmLabel={t.common.delete}
        destructive
        onConfirm={async () => {
          await remove.mutateAsync()
          // Agent 已删除，基本信息里未保存的修改没有意义，直接回到它所在项目或平台的列表。
          void navigate(agentsPath(scope), { state: skipUnsavedPrompt })
        }}
      />
    </Section>
  )
}
