import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'

// Agent 的 Inbox 地址：项目中的 Agent 为完整地址（组织名/项目名/Agent名），平台 Agent 为名称本身，旁边可以复制。
// 名称不满足命名规则时服务端不给地址，这里说明原因：先改名才有地址，发 Inbox 消息时改用内部 ID。
export function AgentAddress({ address }: { address: string | null }) {
  const [copied, setCopied] = useState(false)
  if (!address) return <p className="max-w-[560px] text-[12.5px] leading-snug font-bold text-ink-700">{t.agents.address.missing}</p>
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(address)
    } catch {
      // 浏览器不允许写剪贴板时保持原样，人仍可以手动选中复制。
      return
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  return (
    <div className="flex flex-wrap items-center gap-2.5">
      <span className="sr-only">{t.agents.address.label}</span>
      <span className="font-mono text-[12px] [overflow-wrap:anywhere] text-ink-700 select-all">{address}</span>
      <Button
        size="xs"
        variant="secondary"
        aria-label={`${t.common.copy}${t.agents.address.label}`}
        className="h-6 bg-riso-yellow px-3 text-[12px] hover:bg-riso-yellow"
        onClick={() => void copy()}
      >
        {copied ? t.common.copied : t.common.copy}
      </Button>
    </div>
  )
}
