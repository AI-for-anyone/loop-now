import { useId, useState, useSyncExternalStore } from 'react'
import { Navigate, Outlet, useLocation, useOutletContext } from 'react-router'
import { PlusIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import { reachability } from '@/api/client'
import { useAgents } from '@/api/queries'
import { useScope } from '@/features/projects/scope-context'
import { t } from '@/i18n'
import { isUnreachable } from '@/lib/errors'
import { agentPath, scopeKey, type Scope } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { CreateAgentDialog } from './CreateAgentDialog'

// The Agents page passes “新建 Agent” (New Agent) to the main area's empty state through the route outlet.
interface AgentsOutletContext {
  openCreate: () => void
}

// The Agents page of the current project or the platform. The Agent list is in the sidebar; this only holds the selected
// Agent's page (or, when none is selected, a hint or the first-use empty state).
// When entered from “去新建 Agent” (Create an Agent) in the project settings, the location state carries openCreate and the
// New Agent dialog opens right away.
export function AgentsPage() {
  const scope = useScope()
  const location = useLocation()
  const [creating, setCreating] = useState(() => (location.state as { openCreate?: boolean } | null)?.openCreate === true)
  const openCreate = () => setCreating(true)
  return (
    <section className="h-full min-h-0 min-w-0">
      <Outlet context={{ openCreate } satisfies AgentsOutletContext} />
      {creating && <CreateAgentDialog scope={scope} onClose={() => setCreating(false)} />}
    </section>
  )
}

// The main area while the scope isn't settled yet (project list loading or failed to load): when the server can't be reached,
// nothing extra is said (the global notice and the sidebar skeleton already say it); other errors are shown, and while
// loading only “加载中…” (Loading…) is shown.
export function AgentsPlaceholder({ error }: { error: unknown }) {
  if (isUnreachable(error)) return null
  if (error) return <ErrorAlert error={error} className="mx-6 my-7 w-auto" />
  return <p className="px-6 py-7 text-[14px] text-ink-500">{t.common.loading}</p>
}

// The main area when no Agent is open: with no Agents, show the first-use empty state. In a project that has Agents, go
// straight to the Manager's chat, or to the first Agent in the list when there is no Manager; the platform still asks to pick
// one from the sidebar. Nothing is shown before the list has loaded.
// Only redirect when Agents already exist on opening: for an Agent that appears after the empty state was shown (for example
// one just created), the create flow opens its Config tab itself.
export function AgentsIndex() {
  const { openCreate } = useOutletContext<AgentsOutletContext>()
  const scope = useScope()
  const agents = useAgents(scopeKey(scope))
  const list = agents.data?.agents
  const [sawEmpty, setSawEmpty] = useState(false)
  if (list?.length === 0 && !sawEmpty) setSawEmpty(true)
  if (list?.length === 0) return <FirstUse scope={scope} onCreate={openCreate} />
  if (scope.kind === 'project' && !sawEmpty) {
    if (!list) return null
    const target = list.find((agent) => agent.id === scope.project.managerAgentId) ?? list[0]
    return <Navigate replace to={agentPath(scope, target.id, 'chat')} />
  }
  return (
    <div className="flex h-full min-h-40 items-center justify-center p-6">
      <p className="text-ink-500">{t.agents.selectHint}</p>
    </div>
  )
}

// The first-use empty state: projects use a blue-and-pink overprint and the platform a blue-and-green one, to tell them apart.
function FirstUse({ scope, onCreate }: { scope: Scope; onCreate: () => void }) {
  const platform = scope.kind === 'platform'
  const titleId = useId()
  // Same as the New button in the list header: disabled when the server can't be reached.
  const reachable = useSyncExternalStore(reachability.subscribe, reachability.isReachable)
  // The scroll area is the positioning container: absolutely positioned elements inside it (such as sr-only text) scroll
  // with the content instead of stretching the whole page based on their unscrolled position.
  return (
    <section aria-labelledby={titleId} className="relative flex h-full flex-col items-center justify-center gap-[22px] overflow-y-auto p-6 text-center">
      {/* Overprint illustration: a halftone blue circle, a fluorescent pink (green for the platform) circle and a yellow circle;
          where they overlap they blend with multiply. */}
      <span aria-hidden="true" className="relative h-40 w-[220px] shrink-0">
        <span className="riso-halftone absolute top-2.5 left-2.5 size-[140px] rounded-full bg-riso-blue" />
        <span className={cn('absolute top-0 left-[76px] size-[140px] rounded-full mix-blend-multiply', platform ? 'bg-green' : 'bg-riso-pink')} />
        <span className="absolute top-[92px] left-[150px] size-14 rounded-full bg-riso-yellow mix-blend-multiply" />
      </span>
      <div className="flex flex-col items-center gap-2">
        <h1 id={titleId} className="riso-misreg font-display-cn text-[56px] leading-[1.1] font-normal text-riso-blue">
          {platform ? t.projects.empty.platformTitle : t.agents.empty}
        </h1>
        <p className="text-[14.5px] text-ink-700">{platform ? t.projects.empty.platformNote : t.projects.empty.projectNote}</p>
      </div>
      <Button size="lg" className="h-12 px-[26px] text-base shadow-print-pink" disabled={!reachable} onClick={onCreate}>
        <PlusIcon strokeWidth={3} />
        {t.agents.create}
      </Button>
    </section>
  )
}
