import { useContext, useEffect, useState } from 'react'
import { Link, Navigate, Outlet, useLocation, useNavigate, useParams, type Location } from 'react-router'
import { Button } from '@/components/ui/button'
import { useAgent, useProjects } from '@/api/queries'
import { AgentsPlaceholder } from '@/features/agents/AgentsPage'
import { t } from '@/i18n'
import { isNotFound } from '@/lib/errors'
import { agentPath, agentScope, agentsPath, lastScope, projectBase, renamedHere, resolveLastScope, type Scope } from '@/lib/scope'
import { ScopeContext } from './scope-context'

// Replace the project name in the URL with the new name, keeping the rest of the path, the query parameters and the hash unchanged.
function withProjectName(location: Location, name: string) {
  const rest = location.pathname.split('/').slice(3)
  return `${projectBase(name)}${rest.length ? `/${rest.join('/')}` : ''}${location.search}${location.hash}`
}

// The notice that replaces the whole content when the project can't be found or was deleted (handoff notes, section 11.5).
// There is no sidebar list, and the URL stays as it is.
function ScopeNotice({ kind, name, defaultScope }: { kind: 'missing' | 'deleted'; name: string; defaultScope: Scope | null }) {
  return (
    <div role="alert" className="flex h-full min-h-80 flex-col items-center justify-center gap-3.5 overflow-y-auto p-6 text-center">
      {kind === 'missing' ? (
        // A halftone blue circle plus an empty pink dashed circle: the project you are looking for is gone.
        <span aria-hidden="true" className="relative h-[130px] w-[200px] shrink-0">
          <span className="riso-halftone absolute top-[5px] left-2.5 size-[120px] rounded-full bg-riso-blue" />
          <span className="absolute top-[5px] left-[76px] size-[116px] rounded-full border-[3px] border-dashed border-pink-text" />
        </span>
      ) : (
        <span
          aria-hidden="true"
          className="riso-tilt rounded-stamp px-[18px] py-1 font-display-cn text-[34px] leading-[1.2] text-red-text [--tilt-bg:transparent] [--tilt-shadow:inset_0_0_0_3px_var(--red-text)] [--tilt:-3deg]"
        >
          {t.projects.deleted.stamp}
        </span>
      )}
      <p className="riso-misreg font-display-cn text-[48px] leading-[1.15] text-riso-blue">
        {kind === 'missing' ? t.projects.notFound.title(name) : t.projects.deleted.title(name)}
      </p>
      <p className="text-[15px] text-ink-700">{kind === 'missing' ? t.projects.notFound.body : t.projects.deleted.body}</p>
      {defaultScope && (
        <Button asChild size="lg" className="h-12 px-[26px] text-base shadow-print-pink">
          <Link to={agentsPath(defaultScope)}>{t.projects.toDefault}</Link>
        </Button>
      )}
    </div>
  )
}

// The yellow sticky note shown when the project is renamed elsewhere while you are on it; it stays until you click
// “知道了” (Got it) or leave this project.
function RenamedNote({ from, to, onDismiss }: { from: string; to: string; onDismiss: () => void }) {
  return (
    <div
      role="status"
      className="riso-tilt mx-4 mt-4 flex max-w-[820px] shrink-0 flex-wrap items-center gap-x-[18px] gap-y-2.5 py-3.5 pr-[18px] pl-5 [--tilt-bg:var(--riso-yellow)] [--tilt-shadow:3px_3px_0_rgb(24_38_94/.18)] [--tilt:-0.5deg] sm:mx-[30px]"
    >
      {/* Tape: decoration only. */}
      <span aria-hidden="true" className="hidden [[data-skin=plate]_&]:block absolute -top-[9px] left-7 h-[18px] w-16 -rotate-4 bg-white/60" />
      <span className="flex-[1_1_420px] text-[15px] font-bold">{t.projects.renamed(from, to)}</span>
      <Button variant="ink" size="sm" className="[[data-skin=plate]_&]:text-white" onClick={onDismiss}>
        {t.projects.renamedDismiss}
      </Button>
    </div>
  )
}

// /p/:projectName/…: resolve the name exactly against the project list (case-sensitive); "not found" isn't decided until the
// list has finished loading.
// The resolved project ID is remembered on this page: if the project is renamed elsewhere while you are on it, switch by ID to
// the URL with the new name and put up a sticky note; if it is deleted, say it was deleted.
// When it was renamed in this console (it is in renamedHere), only switch the URL without a sticky note; the project settings
// page explains it itself.
export function ProjectScopeRoute() {
  const { projectName = '' } = useParams()
  const location = useLocation()
  const navigate = useNavigate()
  const projects = useProjects()
  // The project ID resolved on this page and the rename sticky note; updated during render from the previous result, the
  // React way.
  const [resolvedId, setResolvedId] = useState<string | null>(null)
  const [note, setNote] = useState<{ id: string; from: string; to: string } | null>(null)
  const list = projects.data?.projects
  const project = list?.find((p) => p.name === projectName)
  if (project && project.id !== resolvedId) setResolvedId(project.id)
  const followed = !project && resolvedId ? list?.find((p) => p.id === resolvedId) : undefined
  if (followed && !renamedHere.has(followed.id) && note?.to !== followed.name) setNote({ id: followed.id, from: projectName, to: followed.name })
  const current = project ?? followed
  if (note && current && note.id !== current.id) setNote(null)

  useEffect(() => {
    if (project) lastScope.set(project.id)
  }, [project])

  useEffect(() => {
    if (!followed) return
    renamedHere.delete(followed.id)
    void navigate(withProjectName(location, followed.name), { replace: true })
  }, [followed, location, navigate])

  if (!list) return <AgentsPlaceholder error={projects.error} />
  const defaultProject = list.find((p) => p.isDefault)
  const defaultScope: Scope | null = defaultProject ? { kind: 'project', project: defaultProject } : null
  // After a rename and before the URL switches to the new name, the page keeps showing the same project by ID without
  // unmounting the page content.
  if (!current) return <ScopeNotice kind={resolvedId ? 'deleted' : 'missing'} name={projectName} defaultScope={defaultScope} />

  return (
    <ScopeContext.Provider value={{ kind: 'project', project: current }}>
      <div className="flex h-full min-h-0 flex-col">
        {note && <RenamedNote from={note.from} to={note.to} onDismiss={() => setNote(null)} />}
        <div className="min-h-0 flex-1">
          <Outlet />
        </div>
      </div>
    </ScopeContext.Provider>
  )
}

const platformScope: Scope = { kind: 'platform' }

// /platform/…: Agents owned by the platform; they share the Agents page, the Chat tab and the Config tab with projects.
export function PlatformScopeRoute() {
  useEffect(() => {
    lastScope.set('platform')
  }, [])
  return (
    <ScopeContext.Provider value={platformScope}>
      <Outlet />
    </ScopeContext.Provider>
  )
}

// Legacy URL /agents: go to the most recently used project or the platform; when nothing is recorded or the recorded project no
// longer exists, go to the default project.
export function LegacyAgentsRedirect() {
  const location = useLocation()
  const projects = useProjects()
  const scope = projects.data ? resolveLastScope(projects.data.projects) : null
  if (!scope) return <AgentsPlaceholder error={projects.error} />
  return <Navigate replace to={`${agentsPath(scope)}${location.search}${location.hash}`} />
}

// Legacy URL /agents/:agentId/…: load the Agent and switch to the URL where it actually lives, keeping the tab, query parameters
// and hash that follow.
// When the Agent doesn't exist, open the same path in the most recently used project or the platform, and let the detail page
// say that the Agent doesn't exist.
export function LegacyAgentRedirect() {
  const { agentId = '', '*': rest = '' } = useParams()
  const location = useLocation()
  const projects = useProjects()
  const agent = useAgent(agentId)
  const list = projects.data?.projects
  const target = list && agent.data ? agentScope(agent.data, list) : null
  const fallback = list && isNotFound(agent.error) ? resolveLastScope(list) : null
  const scope = target ?? fallback
  if (!scope) return <AgentsPlaceholder error={projects.error ?? (isNotFound(agent.error) ? null : agent.error)} />
  return <Navigate replace to={`${agentsPath(scope)}/${agentId}${rest ? `/${rest}` : ''}${location.search}${location.hash}`} />
}

// /p/<project name> and /platform go to the Agents page; /…/agents/<Agent ID> goes to the Chat tab.
// Navigate with encoded absolute URLs: React Router resolves relative URLs against the decoded path, so a relative URL breaks
// when the project name contains "?" or "%".
export function ScopeIndexRedirect() {
  const scope = useContext(ScopeContext)
  return scope ? <Navigate replace to={agentsPath(scope)} /> : null
}

export function AgentIndexRedirect() {
  const scope = useContext(ScopeContext)
  const { agentId = '' } = useParams()
  const location = useLocation()
  return scope ? <Navigate replace to={`${agentPath(scope, agentId, 'chat')}${location.search}${location.hash}`} /> : null
}
