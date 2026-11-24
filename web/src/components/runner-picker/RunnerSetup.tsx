import { useState } from 'react'
import { ArrowUpRightIcon, CopyIcon, RefreshCwIcon } from 'lucide-react'
import { useRefreshRuntimes } from '@/api/queries'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import { t } from '@/i18n'
import { copyText } from '@/lib/clipboard'
import { runtimeProblemText, type RuntimeDefinition, type RuntimeProblem } from '@/lib/runtime'

// The step to show for a problem, from the commands the server gives for its operating system (RuntimeDefinition.setup): log in when
// logged out, otherwise install (again).
function setupStep(runtime: RuntimeDefinition, problem: RuntimeProblem) {
  const copy = t.runtime.picker.setup
  const setup = runtime.setup
  if (!setup) return null
  if (problem === 'logged_out') return setup.login ? { label: copy.login, command: setup.login } : null
  if (!setup.install) return null
  return { label: problem === 'not_found' ? copy.install : problem === 'version_unsupported' ? copy.update : copy.reinstall, command: setup.install }
}

// A runtime that cannot run on this computer: instead of its models, what is wrong, the command to run in a terminal (with a copy
// button) and the vendor's setup page, and a way to check again once it is done. The picker runs nothing itself. version: the one found
// on this computer, for a CLI that is too old.
export function RunnerSetup({ runtime, problem, version = null }: { runtime: RuntimeDefinition; problem: RuntimeProblem; version?: string | null }) {
  const copy = t.runtime.picker.setup
  const refresh = useRefreshRuntimes()
  const [copied, setCopied] = useState(false)
  const step = setupStep(runtime, problem)
  const docs = runtime.setup?.docsUrl
  return (
    <div className="riso-scrollbar grid min-h-0 content-start gap-3 overflow-y-auto px-4 pt-4 pb-4">
      <h3 className="font-display text-[22px] leading-tight text-ink">{copy.title(runtime.name)}</h3>
      <p className="text-[13.5px] leading-relaxed text-ink-700">{runtimeProblemText(runtime, problem, version)}</p>
      {step && (
        <div className="grid gap-1.5">
          <p className="text-[12px] font-black tracking-[0.04em] text-ink-500">{step.label}</p>
          <div className="flex min-w-0 items-center gap-2 rounded-input border-2 border-dashed border-ink/55 bg-white py-1.5 pr-1.5 pl-3">
            <span aria-hidden="true" className="font-mono text-[13px] font-bold text-riso-pink">$</span>
            <code className="min-w-0 flex-1 py-1 font-mono text-[13px] leading-snug whitespace-pre-wrap text-ink [overflow-wrap:anywhere]">{step.command}</code>
            <Button
              type="button"
              size="xs"
              className="border-2 border-ink bg-riso-yellow text-ink shadow-[2px_2px_0_var(--ink)] hover:bg-riso-yellow"
              onClick={() => void copyText(step.command).then(setCopied)}
            >
              <CopyIcon aria-hidden="true" />
              {copied ? copy.copied : copy.copy}
            </Button>
          </div>
        </div>
      )}
      {docs && (
        <a href={docs} target="_blank" rel="noreferrer" className="inline-flex w-fit items-center gap-1 text-[13px] font-black text-riso-blue underline decoration-2 underline-offset-4 hover:text-ink">
          {copy.docs}
          <ArrowUpRightIcon aria-hidden="true" className="size-3.5" />
        </a>
      )}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-t-2 border-dashed border-ink/25 pt-3">
        <Button type="button" variant="secondary" size="xs" className="border-2 border-ink" disabled={refresh.isPending} onClick={() => refresh.mutate()}>
          <RefreshCwIcon aria-hidden="true" />
          {refresh.isPending ? copy.rechecking : copy.recheck}
        </Button>
        <p className="text-[12.5px] font-bold text-ink-500">{copy.locked(runtime.name)}</p>
      </div>
      <ErrorAlert error={refresh.error} />
    </div>
  )
}
