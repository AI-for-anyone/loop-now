import { useEffect, useState } from 'react'
import { t } from '@/i18n'
import { renderMermaid } from './mermaid-renderer'

export function MermaidBlock({ source, pending }: { source: string; pending: boolean }) {
  const [result, setResult] = useState<{ source: string; svg?: string; failed?: boolean } | null>(null)
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    if (pending) return
    let active = true
    const timer = setTimeout(() => {
      void renderMermaid(source).then(
        svg => { if (active) setResult({ source, svg }) },
        () => { if (active) setResult({ source, failed: true }) },
      )
    }, 300)
    return () => { active = false; clearTimeout(timer) }
  }, [source, pending])

  // Source changes synchronously hide the previous diagram, before effects or async work run.
  const current = !pending && result?.source === source ? result : null
  const copy = async () => {
    await navigator.clipboard.writeText(source)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  return <section className="min-w-0 overflow-hidden rounded-card border border-ink/15 bg-paper-raised" aria-label={t.chat.mermaid.diagram}>
    <div className="flex items-center justify-between gap-3 px-4 py-2">
      <span className="font-mono text-xs">mermaid</span>
      <button type="button" className="inline-flex min-h-7 shrink-0 items-center rounded-full bg-riso-yellow px-3 text-xs font-black text-ink" onClick={() => void copy()}>{copied ? t.common.copied : t.chat.copyCode}</button>
    </div>
    {current?.svg
      ? <div className="overflow-x-auto p-3 [&>svg]:mx-auto [&>svg]:h-auto [&>svg]:max-w-full" data-mermaid-diagram dangerouslySetInnerHTML={{ __html: current.svg }} />
      : <>
        <p className="px-4 text-xs text-ink-500" role="status">{current?.failed ? t.chat.mermaid.failed : pending ? t.chat.mermaid.streaming : t.chat.mermaid.rendering}</p>
        <pre><code className="language-mermaid">{source}</code></pre>
      </>}
  </section>
}
