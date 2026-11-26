import { useEffect, useState } from 'react'
import { t } from '@/i18n'
import { copyText } from '@/lib/clipboard'
import { useTheme } from '@/theme/store'
import { renderMermaid } from './mermaid-renderer'

export function MermaidBlock({ source, pending }: { source: string; pending: boolean }) {
  const { skin, resolvedTheme } = useTheme()
  const [result, setResult] = useState<{ source: string; svg?: string; failed?: boolean } | null>(null)
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    if (pending) return
    let active = true
    const timer = setTimeout(() => {
      void renderMermaid(source, { skin, resolvedTheme }).then(
        svg => { if (active) setResult({ source, svg }) },
        () => { if (active) setResult({ source, failed: true }) },
      )
    }, 300)
    return () => { active = false; clearTimeout(timer) }
  }, [source, pending, skin, resolvedTheme])

  // Source changes synchronously hide the previous diagram, before effects or async work run.
  const current = !pending && result?.source === source ? result : null
  const copy = async () => {
    const ok = await copyText(source)
    if (!ok) return
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  return <section className="min-w-0 overflow-hidden rounded-card border border-code-border bg-code-bg [[data-skin=plate]_&]:border-ink/15 [[data-skin=plate]_&]:bg-paper-raised" aria-label={t.chat.mermaid.diagram}>
    <div className="flex items-center justify-between gap-3 border-b border-code-border px-4 py-2 [[data-skin=plate]_&]:border-b-0">
      <span className="font-mono text-xs text-code-muted [[data-skin=plate]_&]:text-ink">mermaid</span>
      <button type="button" className="inline-flex min-h-7 shrink-0 cursor-pointer items-center rounded-stamp border border-hairline-strong bg-surface-control px-2.5 text-xs font-medium text-ink hover:bg-surface-hover focus-visible:shadow-focus [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:px-3 [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:hover:bg-riso-yellow" onClick={() => void copy()}>{copied ? t.common.copied : t.chat.copyCode}</button>
    </div>
    {current?.svg
      ? <div className="overflow-x-auto p-3 [&>svg]:mx-auto [&>svg]:h-auto [&>svg]:max-w-full" data-mermaid-diagram dangerouslySetInnerHTML={{ __html: current.svg }} />
      : <>
        <p className="px-4 py-2 text-xs text-ink-500" role="status">{current?.failed ? t.chat.mermaid.failed : pending ? t.chat.mermaid.streaming : t.chat.mermaid.rendering}</p>
        <pre><code className="language-mermaid">{source}</code></pre>
      </>}
  </section>
}
