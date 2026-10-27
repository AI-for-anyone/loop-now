import { useId, useState } from 'react'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'

// 内部 ID：只读，等宽显示，旁边是复制按钮，复制成功后短暂显示“已复制”。Agent 与项目的配置页共用。
export function IdField({ label, id }: { label: string; id: string }) {
  const labelId = useId()
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(id)
    } catch {
      // 浏览器不允许写剪贴板时保持原样，人仍可以手动选中复制。
      return
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  return (
    // 列宽可以收缩到容器宽度：ID 不换行，在框内截断，不撑宽所在的区块。
    <div className="grid min-w-0 grid-cols-[minmax(0,1fr)] content-start gap-1.5">
      <span id={labelId} className="text-[13.5px] leading-snug font-black">
        {label}
      </span>
      <div className="flex h-11 items-center gap-2 rounded-input bg-tint-blue pr-1.5 pl-3.5">
        <span className="min-w-0 flex-1 truncate font-mono text-[12.5px] select-all" title={id}>
          {id}
        </span>
        <Button
          type="button"
          size="xs"
          variant="secondary"
          className="h-8 bg-riso-yellow px-3 text-[12.5px] hover:bg-riso-yellow"
          aria-describedby={labelId}
          onClick={() => void copy()}
        >
          {copied ? t.common.copied : t.common.copy}
        </Button>
      </div>
    </div>
  )
}
