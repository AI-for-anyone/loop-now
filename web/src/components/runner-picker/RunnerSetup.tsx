import { useRef, useState } from 'react'
import { Link, useInRouterContext } from 'react-router'
import { ArrowUpRightIcon, CopyIcon, RefreshCwIcon } from 'lucide-react'
import { useAcpCommands } from '@/api/acp-commands'
import { useRefreshRuntimes } from '@/api/queries'
import { ScrollRuler } from '@/components/PrecisionRuler'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import { t } from '@/i18n'
import { copyText } from '@/lib/clipboard'
import { runtimeProblemText, type RuntimeDefinition, type RuntimeProblem } from '@/lib/runtime'
import { runtimeSettingsPath } from '@/lib/scope'

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
  const scrollRef = useRef<HTMLDivElement>(null)
  const [copied, setCopied] = useState(false)
  const step = setupStep(runtime, problem)
  const docs = runtime.setup?.docsUrl
  return (
    <div className="relative flex min-h-0 flex-1 flex-col">
      <div ref={scrollRef} className="no-scrollbar grid max-h-[260px] min-h-0 content-start gap-2.5 overflow-y-auto py-3 pr-6 pl-3.5">
        <h3 className="font-display text-[15px] leading-snug font-semibold tracking-tight text-ink [[data-skin=plate]_&]:text-[18px] [[data-skin=plate]_&]:font-normal [[data-skin=plate]_&]:tracking-normal">
          {copy.title(runtime.name)}
        </h3>
        {runtime.acpCommandId ? (
          <AddedCommandProblem commandId={runtime.acpCommandId} runtime={runtime} problem={problem} version={version} />
        ) : (
          <p className="text-[12.5px] leading-relaxed text-ink-700">{runtimeProblemText(runtime, problem, version)}</p>
        )}
        {step && (
          <div className="grid gap-1">
            <p className="text-[11.5px] font-medium text-ink-500 [[data-skin=plate]_&]:font-black">{step.label}</p>
            <div className="flex min-w-0 items-center gap-2 rounded-input border border-hairline bg-code-bg py-1 pr-1.5 pl-2.5 [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/55 [[data-skin=plate]_&]:bg-white">
              <span aria-hidden="true" className="font-mono text-[12px] font-semibold text-ink-500 [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-riso-pink">
                $
              </span>
              <code className="min-w-0 flex-1 py-0.5 font-mono text-[12px] leading-snug whitespace-pre-wrap text-code-fg [overflow-wrap:anywhere] [[data-skin=plate]_&]:text-ink">{step.command}</code>
              <Button
                type="button"
                size="xs"
                className="[[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:shadow-[2px_2px_0_var(--ink)] [[data-skin=plate]_&]:hover:bg-riso-yellow"
                onClick={() => void copyText(step.command).then(setCopied)}
              >
                <CopyIcon aria-hidden="true" />
                {copied ? copy.copied : copy.copy}
              </Button>
            </div>
          </div>
        )}
        {docs && (
          <a
            href={docs}
            target="_blank"
            rel="noreferrer"
            className="inline-flex w-fit items-center gap-1 text-[12.5px] font-medium text-ink underline decoration-hairline-strong decoration-1 underline-offset-4 hover:decoration-ink [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-riso-blue [[data-skin=plate]_&]:decoration-current [[data-skin=plate]_&]:decoration-2 [[data-skin=plate]_&]:hover:text-ink"
          >
            {copy.docs}
            <ArrowUpRightIcon aria-hidden="true" className="size-3.5" />
          </a>
        )}
        <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1.5 border-t border-hairline pt-2.5 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/25">
          <Button type="button" variant="secondary" size="xs" className="[[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink" disabled={refresh.isPending} onClick={() => refresh.mutate()}>
            <RefreshCwIcon aria-hidden="true" />
            {refresh.isPending ? copy.rechecking : copy.recheck}
          </Button>
          <p className="text-[12px] text-ink-500 [[data-skin=plate]_&]:font-bold">{copy.locked(runtime.name)}</p>
        </div>
        <ErrorAlert error={refresh.error} />
      </div>
      <ScrollRuler targetRef={scrollRef} label={copy.title(runtime.name)} tickCount={11} className="inset-y-2 right-1" />
    </div>
  )
}

// A local ACP command a person added: its program not found is said with the command as entered (the server gives no setup commands for
// it), with a link to where it is edited.
function AddedCommandProblem({ commandId, runtime, problem, version }: { commandId: string; runtime: RuntimeDefinition; problem: RuntimeProblem; version: string | null }) {
  const command = useAcpCommands().data?.commands.find((candidate) => candidate.id === commandId)
  const routed = useInRouterContext()
  const linkClass =
    'inline-flex w-fit items-center gap-1 text-[12.5px] font-medium text-ink underline decoration-hairline-strong decoration-1 underline-offset-4 hover:decoration-ink [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-riso-blue [[data-skin=plate]_&]:decoration-current [[data-skin=plate]_&]:decoration-2 [[data-skin=plate]_&]:hover:text-ink'
  const label = t.runtime.picker.setup.editCommand
  return (
    <>
      <p className="text-[12.5px] leading-relaxed text-ink-700">{problem === 'not_found' ? t.runtime.commands.notFoundNote(command?.command ?? runtime.name) : runtimeProblemText(runtime, problem, version)}</p>
      {routed ? (
        <Link to={runtimeSettingsPath} className={linkClass}>
          {label}
        </Link>
      ) : (
        <a href={runtimeSettingsPath} className={linkClass}>
          {label}
        </a>
      )}
    </>
  )
}
