import { useState } from 'react'
import { CheckIcon, CopyIcon } from 'lucide-react'
import { t } from '@/i18n'

// The Agent's Inbox address: for an Agent in a project it is the full address (org name/project name/Agent name); for a
// platform Agent it is just the name. The address sits in a dashed box, with a small button on the right inside the box that
// copies it; after copying, the icon turns into a check mark and screen readers announce “已复制” (Copied).
// When the name doesn't meet the naming rules the server gives no address, and this explains why: rename the Agent first
// to get an address; until then, use the internal ID to send it Inbox messages.
export function AgentAddress({ address, showSrLabel = true }: { address: string | null; showSrLabel?: boolean }) {
  const [copied, setCopied] = useState(false)
  if (!address) return <p className="max-w-[560px] text-[12.5px] leading-snug font-bold text-ink-700">{t.agents.address.missing}</p>
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(address)
    } catch {
      // If the browser doesn't allow writing to the clipboard, leave things as they are; the person can still select and copy by hand.
      return
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  return (
    <span className="inline-flex max-w-full min-w-0 items-center gap-2 rounded-stamp border-[1.5px] border-dashed border-ink/45 pr-[3px] pl-[9px] text-[12px] leading-[22px]">
      {showSrLabel && <span className="sr-only">{t.agents.address.label}</span>}
      <span className="min-w-0 font-mono [overflow-wrap:anywhere] text-ink-700 select-all">{address}</span>
      <button
        type="button"
        aria-label={t.agents.address.copy}
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
