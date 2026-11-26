import { useEffect, useId, useState, type FormEvent } from 'react'
import { Link, Navigate, useLocation, useNavigate, useParams } from 'react-router'
import { ArrowRightIcon, FolderOpenIcon, PlusIcon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { AgentAvatar } from '@/components/AgentAvatar'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { IdField } from '@/components/IdField'
import { SubNav, type SubNavLinkItem } from '@/components/IndexTabs'
import { ManagerBadge } from '@/components/ManagerBadge'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { PageBody, Sheet } from '@/components/Sheet'
import { SheetSection } from '@/components/SheetSections'
import { ApiError, type Schemas } from '@/api/client'
import { useAgents, useClearManager, useDeleteProject, useProjects, useRenameProject, useSetManager } from '@/api/queries'
import { tasksPath } from '@/api/tasks'
import { t } from '@/i18n'
import { isAgentNameConflict, projectNameError, unmatchedFieldError } from '@/lib/errors'
import { nameRuleError } from '@/lib/names'
import { agentPath, agentsPath, overviewPath, projectBase, projectSettingsPath, type ProjectSettingsTab, type Scope } from '@/lib/scope'
import { useScope } from './scope-context'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { ProjectPromptForm } from '@/features/prompts/ProjectPromptForm'
import { ProjectCredentialSettings } from '@/features/project-credentials/ProjectAccounts'
import { githubLocatorKey } from '@/features/project-credentials/github-authorization'
import { larkLoginLocatorKey } from '@/lib/useOriginalActionLocator'
import { ProjectLocalDirectories } from '@/features/local-mode/LocalDirectories'
import { ProjectPluginsPanel } from '@/features/plugins/ProjectPluginsPage'
import { BackgroundCountTag } from '@/features/chat/BackgroundTasks'

function hasCredentialRecoveryParams(params: URLSearchParams, projectId: string): boolean {
  if (params.has('cliImport')) return true
  const ghPrefix = githubLocatorKey(projectId).replace(/new$/, '')
  const larkPrefix = larkLoginLocatorKey(projectId).replace(/new$/, '')
  for (const key of params.keys()) {
    if (key.startsWith(ghPrefix) || key.startsWith(larkPrefix)) return true
  }
  return false
}

const VALID_TABS = new Set<ProjectSettingsTab>(['general', 'context', 'credentials', 'plugins', 'danger'])

// Legacy #settings-* hashes mapped to their owning tab so old links continue landing on the right section.
const LEGACY_HASH_TAB: Record<string, ProjectSettingsTab> = {
  '#settings-name': 'general',
  '#settings-manager': 'general',
  '#settings-prompt': 'context',
  '#settings-folder': 'context',
  '#settings-local-dirs': 'context',
  '#settings-accounts': 'credentials',
  '#settings-plugins': 'plugins',
  '#settings-danger': 'danger',
}

// Project settings: URL-backed SubNav tabs replacing the legacy 8-section scroll wall and 196px SectionToc stub.
// DraftNavigationGuard wraps the outer shell so switching tabs or leaving protects unsaved drafts.
export function ProjectSettingsPage() {
  const scope = useScope()
  const { tab: rawTab } = useParams<{ tab?: string }>()
  const { search, hash } = useLocation()

  const activeTab: ProjectSettingsTab =
    rawTab && VALID_TABS.has(rawTab as ProjectSettingsTab) ? (rawTab as ProjectSettingsTab) : 'general'

  useEffect(() => {
    if (!hash) return
    const target = document.getElementById(hash.slice(1))
    target?.scrollIntoView({ block: 'start' })
  }, [activeTab, hash])

  if (scope.kind !== 'project') return null
  const project = scope.project

  // Canonicalize explicit /settings/general or unknown sub-routes back to /settings.
  if (rawTab && (rawTab === 'general' || !VALID_TABS.has(rawTab as ProjectSettingsTab))) {
    return <Navigate to={`${projectSettingsPath(project)}${search}${hash}`} replace />
  }

  // Forward legacy index URLs carrying credential recovery params (cliImport, gh/lark OAuth locators) or legacy #settings-* hashes.
  if (!rawTab) {
    const params = new URLSearchParams(search)
    if (hasCredentialRecoveryParams(params, project.id)) {
      return <Navigate to={`${projectSettingsPath(project, 'credentials')}${search}${hash}`} replace />
    }
    const hashTab = LEGACY_HASH_TAB[hash]
    if (hashTab && hashTab !== 'general') {
      return <Navigate to={`${projectSettingsPath(project, hashTab)}${search}${hash}`} replace />
    }
  }

  const tabsCopy = t.projects.settings.tabs
  const navItems: SubNavLinkItem[] = [
    { key: 'general', label: tabsCopy.general, to: projectSettingsPath(project), end: true },
    { key: 'context', label: tabsCopy.context, to: projectSettingsPath(project, 'context') },
    { key: 'credentials', label: tabsCopy.credentials, to: projectSettingsPath(project, 'credentials') },
    { key: 'plugins', label: tabsCopy.plugins, to: projectSettingsPath(project, 'plugins') },
    { key: 'danger', label: tabsCopy.danger, to: projectSettingsPath(project, 'danger') },
  ]

  return (
    <DraftNavigationGuard key={project.id} projectName={project.name}>
      <PageFrame>
        <PageHeader crumbs={[{ label: project.name, to: overviewPath({ kind: 'project', name: project.name }) }]} title={t.projects.settings.title}>
          {project.isDefault && <span className="shrink-0 rounded-full bg-cta-bg px-2.5 text-[12px] leading-[22px] font-semibold text-cta-fg [[data-skin=plate]_&]:bg-ink [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-white">{t.projects.settings.defaultBadge}</span>}
          <span className="min-w-0 font-mono text-[12px] [overflow-wrap:anywhere] text-ink-700">{project.id}</span>
        </PageHeader>
        <SubNav aria-label={tabsCopy.nav} items={navItems} />
        <Sheet className="flex-1">
          {activeTab === 'general' && (
            <PageBody width="narrow" innerClassName="flex flex-col gap-6 pb-12">
              {/* Remount by project ID: inputs and messages don't carry over to another project; after a rename the ID is
                  the same, so Saved stays. */}
              <NameCard key={`name-${project.id}`} project={project} />
              <ManagerCard key={`manager-${project.id}`} project={project} scope={scope} />
            </PageBody>
          )}
          {activeTab === 'context' && (
            <PageBody width="narrow" innerClassName="flex flex-col gap-6 pb-12">
              <SheetSection id="settings-prompt" titleId="settings-prompt-title" index={0} title={t.prompts.projectTitle}>
                <ProjectPromptForm projectId={project.id} />
              </SheetSection>
              <SheetSection id="settings-folder" titleId="settings-folder-title" index={1} title={t.folders.project.title}>
                <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2.5 rounded-card border border-hairline bg-surface-control px-4 py-3 shadow-surface [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-white [[data-skin=plate]_&]:shadow-print-rest">
                  <div className="flex min-w-0 flex-[1_1_240px] items-start gap-2.5">
                    <FolderOpenIcon aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-ink-500" />
                    <p className="min-w-0 text-[13.5px] leading-snug text-ink-700">{t.folders.project.intro}</p>
                  </div>
                  <Button asChild size="sm" variant="secondary" className="shrink-0">
                    <Link to={`${projectBase(project.name)}/folder`}>
                      {t.projects.settings.folderCard.open}
                      <ArrowRightIcon aria-hidden="true" className="size-3.5" />
                    </Link>
                  </Button>
                </div>
              </SheetSection>
              <SheetSection id="settings-local-dirs" titleId="settings-local-dirs-title" index={2} title={t.localMode.directories.title}>
                <ProjectLocalDirectories key={`local-dirs-${project.id}`} projectId={project.id} />
              </SheetSection>
            </PageBody>
          )}
          {activeTab === 'credentials' && (
            <PageBody width="default" innerClassName="flex flex-col gap-5 pb-12">
              <div id="settings-accounts" className="min-w-0">
                <ProjectCredentialSettings key={`accounts-${project.id}`} projectId={project.id} embedded />
              </div>
            </PageBody>
          )}
          {activeTab === 'plugins' && (
            <PageBody width="default" innerClassName="flex flex-col gap-4 pb-12">
              <div id="settings-plugins" className="flex min-w-0 flex-col gap-4">
                <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
                  <p className="min-w-0 flex-[1_1_280px] text-[13.5px] text-ink-700">{t.plugins.project.intro(project.name)}</p>
                  <Button asChild size="xs" variant="secondary">
                    <Link to="/plugins">{t.plugins.project.market}</Link>
                  </Button>
                </div>
                <ProjectPluginsPanel project={project} />
              </div>
            </PageBody>
          )}
          {activeTab === 'danger' && (
            <PageBody width="narrow" innerClassName="flex flex-col gap-6 pb-12">
              {project.isDefault ? (
                <SheetSection id="settings-danger" titleId="settings-danger-title" index={0} title={t.projects.settings.danger.title}>
                  <p className="text-[14.5px]">{t.projects.settings.danger.defaultNote}</p>
                </SheetSection>
              ) : (
                <DangerCard key={`danger-${project.id}`} project={project} />
              )}
            </PageBody>
          )}
        </Sheet>
      </PageFrame>
    </DraftNavigationGuard>
  )
}

// Rename: checked locally against the naming rules first; once saved, the project list has the new name and ProjectScopeRoute
// switches the URL to the new name by ID (without putting up the "renamed elsewhere" sticky note).
function NameCard({ project }: { project: Schemas['Project'] }) {
  const rename = useRenameProject(project.id)
  const hintId = useId()
  const [name, setName] = useState(project.name)
  const [localError, setLocalError] = useState<string | undefined>()
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (name === project.name) return
    const rule = name.trim() ? nameRuleError(name) : t.projects.create.nameRequired
    setLocalError(rule)
    if (!rule) rename.mutate({ name })
  }
  const nameError = localError ?? projectNameError(rename.error)
  const saved = rename.isSuccess && rename.data.name === project.name && name === project.name
  return (
    <SheetSection id="settings-name" titleId="settings-name-title" index={0} title={t.projects.settings.name.title}>
      <form className="flex flex-col gap-4" onSubmit={submit} noValidate>
        <div className="grid grid-cols-[repeat(auto-fit,minmax(min(300px,100%),1fr))] items-start gap-4">
          <IdField label={t.projects.settings.name.id} id={project.id} />
          <div className="grid gap-1.5">
            <Label htmlFor="project-name">{t.projects.settings.name.label}</Label>
            <Input
              id="project-name"
              value={name}
              aria-invalid={nameError ? true : undefined}
              aria-describedby={hintId}
              onChange={(e) => setName(e.target.value)}
            />
            {nameError && (
              <p role="alert" className="text-[12.5px] leading-snug font-medium text-red-text [[data-skin=plate]_&]:font-bold">
                {nameError}
              </p>
            )}
            <p id={hintId} className="text-[12.5px] leading-snug text-ink-500">
              {t.projects.settings.name.hint}
            </p>
          </div>
        </div>
        {!isAgentNameConflict(rename.error) && <ErrorAlert error={unmatchedFieldError(rename.error, (field) => field === 'name')} />}
        <div className="flex flex-wrap items-center justify-end gap-x-4 gap-y-2.5">
          {saved && (
            <span role="status" className="mr-auto inline-flex flex-wrap items-center gap-2">
              <Badge tone="success">{t.projects.settings.name.saved}</Badge>
              <span className="text-[12.5px] text-ink-700">
                {t.projects.settings.name.movedTo} <span className="font-mono">{decodeURIComponent(projectSettingsPath(project))}</span>
              </span>
            </span>
          )}
          <Button type="submit" size="lg" disabled={rename.isPending || name === project.name}>
            {t.common.save}
          </Button>
        </div>
      </form>
    </SheetSection>
  )
}

// Busy or idle; an idle Agent whose background tasks still run says so next to it.
const statusBadge = (agent: Schemas['AgentListItem']) =>
  agent.busy ? (
    <Badge tone="busy">{t.agents.busy}</Badge>
  ) : (
    <span className="inline-flex flex-wrap items-center gap-1.5">
      <Badge tone="success">{t.agents.idle}</Badge>
      <BackgroundCountTag count={agent.backgroundTaskCount} />
    </span>
  )

// Manager: assign, change or remove it from among this project's Agents; takes effect immediately with no confirmation
// dialog, and the result is shown as a green message. An Agent can be assigned even while it is busy.
function ManagerCard({ project, scope }: { project: Schemas['Project']; scope: Scope }) {
  const navigate = useNavigate()
  const labelId = useId()
  const agents = useAgents(project.id)
  const setManager = useSetManager(project.id)
  const clearManager = useClearManager(project.id)
  const [result, setResult] = useState<string | null>(null)
  const list = agents.data?.agents ?? []
  const manager = list.find((agent) => agent.id === project.managerAgentId)
  const pending = setManager.isPending || clearManager.isPending

  const choose = (agentId: string) => {
    if (agentId === project.managerAgentId) return
    const name = list.find((agent) => agent.id === agentId)?.name ?? agentId
    setResult(null)
    setManager.mutate(agentId, { onSuccess: () => setResult(t.projects.settings.manager.assigned(name)) })
  }
  const clear = () => {
    setResult(null)
    clearManager.mutate(undefined, { onSuccess: () => setResult(t.projects.settings.manager.cleared) })
  }

  return (
    <SheetSection id="settings-manager" titleId="settings-manager-title" index={1} title={t.projects.settings.manager.title}>
      <p className="text-[14px] text-ink-700">{t.projects.settings.manager.intro}</p>
      {manager ? (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-3 rounded-card border border-hairline bg-surface-control px-4 py-3.5 shadow-surface [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-white [[data-skin=plate]_&]:shadow-print-rest">
          <AgentAvatar id={manager.id} />
          <span className="flex min-w-0 flex-[1_1_220px] flex-col">
            <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
              <Link
                to={agentPath(scope, manager.id, 'chat')}
                className="text-[16px] font-semibold text-ink underline decoration-ink decoration-1 underline-offset-4 hover:text-ink-700 [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:decoration-riso-pink [[data-skin=plate]_&]:decoration-2"
              >
                {manager.name}
              </Link>
              <ManagerBadge />
              {statusBadge(manager)}
            </span>
            <span className="font-mono text-[11.5px] text-ink-500">{`${manager.model.provider} / ${manager.model.modelId}`}</span>
          </span>
          <Button variant="ghost" size="sm" disabled={pending} onClick={clear}>
            {t.projects.settings.manager.clear}
          </Button>
        </div>
      ) : (
        <div role="status" className="flex items-center gap-2.5 rounded-card border border-hairline bg-surface-subtle px-4 py-3 text-[14.5px] font-medium [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue [[data-skin=plate]_&]:font-black">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.6} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" className="size-4 shrink-0">
            <path d="M5 21V4h11l-2 4 2 4H5" />
          </svg>
          {t.projects.settings.manager.none}
        </div>
      )}
      {agents.isSuccess && list.length === 0 ? (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2.5">
          <span className="text-[14.5px]">{t.projects.settings.manager.noAgents}</span>
          <Button variant="ink" size="sm" onClick={() => void navigate(agentsPath(scope), { state: { openCreate: true } })}>
            <PlusIcon strokeWidth={3} />
            {t.projects.settings.manager.createAgent}
          </Button>
        </div>
      ) : (
        <div className="grid max-w-[420px] gap-1.5">
          <span id={labelId} className="text-[13.5px] leading-snug font-semibold [[data-skin=plate]_&]:font-black">
            {manager ? t.projects.settings.manager.change : t.projects.settings.manager.choose}
          </span>
          <Combobox
            aria-labelledby={labelId}
            value={manager ? manager.id : null}
            options={list.map((agent) => ({ value: agent.id, label: agent.name, hint: agent.model.modelId }))}
            onChange={choose}
            placeholder={t.projects.settings.manager.placeholder}
            filterLabel={t.projects.settings.manager.filter}
            emptyText={t.projects.settings.manager.noMatch}
            disabled={pending || !agents.isSuccess}
          />
        </div>
      )}
      {result && (
        <span role="status" className="self-start">
          <Badge tone="success">{result}</Badge>
        </span>
      )}
      <ErrorAlert error={setManager.error ?? clearManager.error ?? agents.error} />
    </SheetSection>
  )
}

const busyAgentsOf = (error: unknown): { id: string; name: string }[] | null =>
  error instanceof ApiError && error.code === 'agent_not_idle' && error.details && 'agents' in error.details ? error.details.agents : null

// Delete project: four notes and the delete button; the confirmation dialog states how many Agents will be deleted, and focus
// starts on Cancel.
// When the server refuses because some Agents aren't idle, close the confirmation dialog and list those Agents in this card,
// keeping them until the next click on delete or until leaving the page; on success, go to the default project.
function DangerCard({ project }: { project: Schemas['Project'] }) {
  const navigate = useNavigate()
  const projects = useProjects()
  const remove = useDeleteProject(project.id)
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState<{ id: string; name: string }[] | null>(null)
  const [activeTasks, setActiveTasks] = useState<Schemas['ProjectActiveTasksDetails']['tasks'] | null>(null)
  const defaultProject = projects.data?.projects.find((p) => p.isDefault)
  const scope: Scope = { kind: 'project', project }

  return (
    <SheetSection id="settings-danger" titleId="settings-danger-title" index={0} title={t.projects.settings.danger.title} tone="danger">
      <ul className="list-disc pl-5 text-[14.5px] leading-[1.8]">
        {t.projects.settings.danger.notes.map((note) => (
          <li key={note}>{note}</li>
        ))}
      </ul>
      <div>
        <Button
          variant="destructive"
          onClick={() => {
            setBusy(null)
            setActiveTasks(null)
            setConfirming(true)
          }}
        >
          {t.projects.settings.danger.action}
        </Button>
      </div>
      {activeTasks && <div role="alert" className="grid gap-2 rounded-card border border-hairline bg-surface-control p-4 text-[14px] [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-white">
        <p className="font-semibold text-red-text [[data-skin=plate]_&]:font-black">{t.credentials.projectSettings.deleteBlocked}</p>
        <ul className="grid gap-2">{activeTasks.map(task => <li key={task.id}><Link className="inline-flex min-h-9 items-center font-medium underline [overflow-wrap:anywhere] [[data-skin=plate]_&]:font-bold" to={`${tasksPath(project.name)}/${task.id}`}>{t.credentials.projectSettings.viewTaskPrefix}{task.id} · {task.status}</Link></li>)}</ul>
      </div>}
      {busy && (
        <div role="alert" className="flex flex-col gap-2.5 rounded-card border border-red/40 bg-surface-control px-[18px] py-4 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-white [[data-skin=plate]_&]:shadow-[inset_0_0_0_2px_var(--red)]">
          <span className="text-[15px] font-semibold text-red-text [[data-skin=plate]_&]:font-black">{t.projects.settings.danger.notIdleTitle}</span>
          <span className="text-[14px] text-ink-700">{t.projects.settings.danger.notIdleBody}</span>
          <ul aria-label={t.projects.settings.danger.busyList} className="flex flex-col gap-2">
            {busy.map((agent) => (
              <li key={agent.id} className="flex items-center gap-3 rounded-ticket bg-error-field px-3 py-2">
                <AgentAvatar id={agent.id} />
                <span className="min-w-0 flex-1 truncate text-[14.5px] font-semibold [[data-skin=plate]_&]:font-black">{agent.name}</span>
                <Link
                  to={agentPath(scope, agent.id, 'chat')}
                  aria-label={t.projects.settings.danger.openChatOf(agent.name)}
                  className="inline-flex min-h-9 shrink-0 items-center gap-1.5 text-[13.5px] font-medium text-ink underline decoration-ink decoration-1 underline-offset-4 hover:text-ink-700 [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:decoration-riso-pink [[data-skin=plate]_&]:decoration-2"
                >
                  {t.projects.settings.danger.openChat}
                  <ArrowRightIcon className="size-3.5" strokeWidth={2.6} />
                </Link>
              </li>
            ))}
          </ul>
        </div>
      )}
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t.projects.settings.danger.confirmTitle(project.name)}
        body={t.projects.settings.danger.confirmBody(project.agentCount)}
        confirmLabel={t.projects.settings.danger.confirm}
        destructive
        onConfirm={async () => {
          try {
            await remove.mutateAsync()
          } catch (error) {
            if (error instanceof ApiError && error.code === 'task_not_idle' && error.details && 'tasks' in error.details) {
              const tasks = error.details.tasks
              const valid = (task: { id: string; status: string }): task is Schemas['ProjectActiveTasksDetails']['tasks'][number] => ['preparing', 'running', 'waiting', 'blocked', 'completed', 'cancelled'].includes(task.status)
              if (!tasks.every(valid)) throw error
              setActiveTasks(tasks)
              return
            }
            const agents = busyAgentsOf(error)
            if (!agents) throw error
            setBusy(agents)
            return
          }
          if (defaultProject) void navigate(agentsPath({ kind: 'project', project: defaultProject }))
        }}
      />
    </SheetSection>
  )
}
