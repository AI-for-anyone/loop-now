import { useState } from 'react'
import { CheckIcon, CopyIcon } from 'lucide-react'
import { t } from '@/i18n'

// Agent 的 Inbox 地址：项目中的 Agent 为完整地址（组织名/项目名/Agent名），平台 Agent 为名称本身。地址写在虚线框里，
// 框内右侧一个小按钮复制它；复制后图标换成对勾并读出“已复制”。
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
    <span className="inline-flex max-w-full min-w-0 items-center gap-2 rounded-stamp border-[1.5px] border-dashed border-ink/45 pr-[3px] pl-[9px] text-[12px] leading-[22px]">
      <span className="sr-only">{t.agents.address.label}</span>
      <span className="min-w-0 font-mono [overflow-wrap:anywhere] text-ink-700 select-all">{address}</span>
      <button
        type="button"
        aria-label={`${t.common.copy}${t.agents.address.label}`}
        title={copied ? t.common.copied : t.common.copy}
        onClick={() => void copy()}
        className="inline-flex size-[18px] shrink-0 cursor-pointer items-center justify-center rounded-stamp text-ink outline-none hover:bg-tint-blue focus-visible:shadow-focus"
      >
        {copied ? <CheckIcon aria-hidden="true" className="size-3" strokeWidth={3} /> : <CopyIcon aria-hidden="true" className="size-3" strokeWidth={2.4} />}
        <span role="status" className="sr-only">
          {copied ? t.common.copied : ''}
        </span>
      </button>
    </span>
  )
}
