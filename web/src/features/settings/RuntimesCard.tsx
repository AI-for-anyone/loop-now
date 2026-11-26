import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { Schemas } from '@/api/client'
import { useRefreshRuntimes, useRuntimeDefinitions, useRuntimes } from '@/api/queries'
import { t } from '@/i18n'
import { useAcpCommands } from '@/api/acp-commands'
import { runtimeProblem, runtimeProblemText, useRuntimeLookup, type RuntimeDefinition } from '@/lib/runtime'
import { AcpCommandsSection } from './AcpCommands'

const text = t.runtime.status
const loginTone = { logged_in: 'success', logged_out: 'warning', unknown: 'neutral' } as const

// Agent runtimes in global settings: a bundled runtime (Pi) is installed with the platform; for one installed on this computer (Claude
// Code), check whether it exists, whether its version can be read, and whether it is logged in.
// The server caches for 30 seconds; “重新检查” (Check again) bypasses the cache. For the login, only the method and the
// subscription type are given; the server doesn't return the email or the organization.
// After the redesign this is the content of one numbered section on the global settings sheet: a row with the description and
// “重新检查” (Check again), with one card per runtime below. The runtimes of the local ACP commands people added are not among those
// cards: they are listed with their commands at the end (AcpCommands.tsx).
export function RuntimesCard() {
  const runtimes = useRuntimes()
  const lookup = useRuntimeLookup()
  const added = new Set(useAcpCommands().data?.commands.map((command) => command.runtime))
  const isAdded = (id: string) => added.has(id) || Boolean(lookup.get(id)?.acpCommandId)
  const definitions = useRuntimeDefinitions().data?.runtimes.filter((definition) => !isAdded(definition.id))
  const refresh = useRefreshRuntimes()
  return (
    <div className="grid gap-3.5">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2.5">
        <p className="min-w-0 flex-[1_1_280px] text-[13.5px] text-ink-700">{definitions && introOf(definitions)}</p>
        <Button
          size="sm"
          variant="secondary"
          className="h-[34px] px-5 text-[13.5px] [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:font-black"
          disabled={refresh.isPending || runtimes.isPending}
          onClick={() => refresh.mutate()}
        >
          {refresh.isPending ? text.refreshing : text.refresh}
        </Button>
      </div>
      <ErrorAlert error={refresh.error ?? runtimes.error} />
      {runtimes.isPending && <p className="text-[13.5px] text-ink-500">{t.common.loading}</p>}
      {runtimes.data?.runtimes.filter((runtime) => !isAdded(runtime.id)).map((runtime) => <RuntimeRow key={runtime.id} runtime={runtime} />)}
      <AcpCommandsSection />
    </div>
  )
}

// One sentence per runtime: the runtime's own when the copy has one, otherwise by what it is.
function introOf(definitions: readonly RuntimeDefinition[]): string {
  const intro = text.intro
  return definitions
    .map(({ id, name, capabilities }) => intro.byRuntime[id] ?? (capabilities.bundled ? intro.bundled(name) : capabilities.accounts === 'local' ? intro.local(name) : intro.installed(name)))
    .join(intro.separator)
}

function RuntimeRow({ runtime }: { runtime: Schemas['RuntimeStatus'] }) {
  const lookup = useRuntimeLookup()
  const name = lookup.name(runtime.id)
  const problem = runtimeProblem(runtime)
  const login = runtime.login
  const method = login?.authMethod ? (text.authMethods[login.authMethod] ?? login.authMethod) : null
  const account = login ? [method, login.subscriptionType && text.subscription(login.subscriptionType)].filter(Boolean).join(' · ') : ''
  return (
    // Redesign: a white card with a 2px navy outline and a solid navy offset shadow; the name is in display type, followed by
    // a status pill and the version number in monospace.
    <section aria-label={name} className="flex min-w-0 flex-col gap-2.5 rounded-card border border-hairline-strong bg-surface-control px-[18px] py-3.5 shadow-surface [[data-skin=plate]_&]:mr-[5px] [[data-skin=plate]_&]:mb-[5px] [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-white [[data-skin=plate]_&]:shadow-[5px_5px_0_var(--ink)]">
      <div className="flex flex-wrap items-center gap-x-3.5 gap-y-2">
        <span className="font-display-cn text-[26px] leading-[1.1]">{name}</span>
        {runtime.version && (
          <span className="text-[12.5px] text-ink-700">
            {text.version} <span className="font-mono text-ink">{runtime.version}</span>
          </span>
        )}
        <span className="ml-auto flex flex-wrap gap-1.5">
          <Badge tone={runtime.available ? 'success' : 'danger'}>{runtime.available ? text.available : text.unavailable}</Badge>
          {login && <Badge tone={loginTone[login.state]}>{text.login[login.state]}</Badge>}
        </span>
      </div>
      {(runtime.binaryPath || account) && (
        <dl className="grid grid-cols-[minmax(60px,auto)_minmax(0,1fr)] gap-x-3 gap-y-1 text-[13.5px]">
          {runtime.binaryPath && <><dt className="font-black text-ink-500">{text.path}</dt><dd className="min-w-0 font-mono text-[12.5px] break-all">{runtime.binaryPath}</dd></>}
          {account && <><dt className="font-black text-ink-500">{text.account}</dt><dd className="min-w-0 break-words">{account}</dd></>}
        </dl>
      )}
      {problem && (
        <Alert variant="warning" role="note" className="text-[13.5px]">
          <AlertDescription className="text-[13.5px] text-ink">{runtimeProblemText({ id: runtime.id, name, minVersion: lookup.get(runtime.id)?.minVersion ?? null }, problem, runtime.version)}</AlertDescription>
        </Alert>
      )}
      {login?.state === 'unknown' && <p className="text-[13px] text-ink-700">{text.unknownLogin}</p>}
      <p className="text-[12px] text-ink-500">{text.checkedAt(t.chat.time(runtime.checkedAt))}</p>
    </section>
  )
}
