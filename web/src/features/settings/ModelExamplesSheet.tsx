import { useState } from 'react'
import { BookOpenIcon, CheckIcon, CopyIcon } from 'lucide-react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { t } from '@/i18n'
import { exampleJSON, mergeExample, MODEL_EXAMPLES, type ModelExample } from './model-examples'

interface ModelExamplesSheetProps {
  open: boolean
  draft: string
  onOpenChange: (open: boolean) => void
  onInsert: (text: string, title: string) => void
}

export function ModelExamplesSheet({ open, draft, onOpenChange, onInsert }: ModelExamplesSheetProps) {
  const copy = t.settings.models.examples
  const [conflict, setConflict] = useState<{ example: ModelExample; providers: string[] } | null>(null)
  const [message, setMessage] = useState<string | null>(null)
  const [copied, setCopied] = useState<string | null>(null)

  const insert = (example: ModelExample, overwrite = false) => {
    const result = mergeExample(draft, example, overwrite)
    if (result.kind === 'invalid') {
      setConflict(null)
      setMessage(copy.invalid[result.reason])
    } else if (result.kind === 'conflict') {
      setMessage(null)
      setConflict({ example, providers: result.providers })
    } else {
      setConflict(null)
      setMessage(null)
      onInsert(result.text, copy.items[example.id].title)
      onOpenChange(false)
    }
  }

  const copyExample = async (example: ModelExample) => {
    try {
      await navigator.clipboard.writeText(exampleJSON(example))
      setCopied(example.id)
      setMessage(null)
    } catch {
      setCopied(null)
      setMessage(copy.copyFailed)
    }
  }

  return <Sheet open={open} onOpenChange={next => { setConflict(null); setMessage(null); onOpenChange(next) }}>
    <SheetContent aria-label={copy.title}>
      <SheetHeader>
        <div className="flex items-center gap-2"><BookOpenIcon className="size-5" /><SheetTitle>{copy.title}</SheetTitle></div>
        <SheetDescription>{copy.description}</SheetDescription>
      </SheetHeader>
      <SheetBody>
        <section className="grid gap-2 rounded-card bg-tint-blue p-4 text-[13.5px] leading-[1.7]" aria-label={copy.fieldGuide}>
          <h3 className="font-black">{copy.fieldGuide}</h3>
          <p><strong>api</strong>{copy.field(copy.apiHelp)}</p>
          <p><strong>input</strong>{copy.field(copy.inputHelp)}</p>
          <p><strong>reasoning</strong>{copy.field(copy.reasoningHelp)}</p>
          <p><strong>contextWindow / maxTokens</strong>{copy.field(copy.tokensHelp)}</p>
          <p><strong>apiKey</strong>{copy.field(copy.apiKeyHelp)}</p>
        </section>
        {message && <Alert variant="warning" role="alert"><AlertDescription>{message}</AlertDescription></Alert>}
        {MODEL_EXAMPLES.map(example => {
          const meta = copy.items[example.id]
          return <section key={example.id} className="grid gap-3 rounded-card bg-white p-4 shadow-print-rest" aria-label={meta.title}>
            <div className="grid gap-1">
              <h3 className="text-[16px] font-black">{meta.title}</h3>
              <p className="text-[13px] leading-[1.7] text-ink-500">{meta.description}</p>
            </div>
            <pre className="max-h-[420px] overflow-auto rounded-card bg-ink p-3 font-mono text-[12px] leading-[1.65] text-white"><code>{exampleJSON(example)}</code></pre>
            <div className="flex flex-wrap gap-2">
              <Button size="sm" variant="secondary" onClick={() => void copyExample(example)}>{copied === example.id ? <CheckIcon /> : <CopyIcon />}{copied === example.id ? copy.copied : copy.copy}</Button>
              <Button size="sm" onClick={() => insert(example)}>{copy.insert}</Button>
            </div>
            {conflict?.example.id === example.id && <Alert variant="warning" role="alert"><AlertDescription className="grid gap-3">
              <span>{copy.conflict(conflict.providers.join(copy.listSeparator))}</span>
              <span className="flex flex-wrap gap-2">
                <Button size="sm" variant="destructive" onClick={() => insert(example, true)}>{copy.overwrite}</Button>
                <Button size="sm" variant="secondary" onClick={() => setConflict(null)}>{t.common.cancel}</Button>
              </span>
            </AlertDescription></Alert>}
          </section>
        })}
      </SheetBody>
    </SheetContent>
  </Sheet>
}
