import { credentialText } from '@/i18n/credential-text'
import { useId, useState, type FormEvent, type ReactNode } from 'react'
import { Link, useNavigate } from 'react-router'
import { ArrowRightIcon, PlusIcon, SettingsIcon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { AgentAvatar } from '@/components/AgentAvatar'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { IdField } from '@/components/IdField'
import { ManagerBadge } from '@/components/ManagerBadge'
import { ApiError, type Schemas } from '@/api/client'
import { useAgents, useClearManager, useDeleteProject, useProjects, useRenameProject, useSetManager } from '@/api/queries'
import { t } from '@/i18n'
import { isAgentNameConflict, projectNameError, unmatchedFieldError } from '@/lib/errors'
import { nameRuleError } from '@/lib/names'
import { agentPath, agentsPath, projectSettingsPath, type Scope } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { useScope } from './scope-context'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { ProjectPromptForm } from '@/features/prompts/ProjectPromptForm'
import { ProjectCredentialSettings } from '@/features/project-credentials/ProjectAccounts'
import { tasksPath } from '@/api/tasks'
import { ContextFolderSection } from '@/features/folders/FolderSections'
import { pageTitleClass } from '@/lib/page-title'

// 区块卡片，与配置页的区块相同：纸白底、18 圆角、轻投影，标题为 ZCOOL 30。
// danger 为红色斜纹的危险卡片；neutral 为浅描边的中性卡片（默认项目的删除区）。
function Card({ title, tone = 'default', children }: { title: string; tone?: 'default' | 'danger' | 'neutral'; children: ReactNode }) {
  const titleId = useId()
  return (
    <section
      aria-labelledby={titleId}
      className={cn(
        'flex flex-col gap-3.5 rounded-panel px-6 py-[22px] text-ink',
        tone === 'danger' && 'riso-stripes-red bg-error-field shadow-print-rest',
        tone === 'neutral' && 'bg-paper-raised shadow-[inset_0_0_0_2px_rgb(24_38_94/.12)]',
        tone === 'default' && 'bg-paper-raised shadow-print-rest',
      )}
    >
      <h2
        id={titleId}
        className={cn('font-display-cn text-[30px] leading-[1.15] font-normal', tone === 'danger' && 'text-red-text', tone === 'neutral' && 'text-ink-700')}
      >
        {title}
      </h2>
      {children}
    </section>
  )
}

const DefaultTag = () => (
  <span className="rounded-stamp bg-white px-[7px] text-[12px] leading-5 font-black text-ink shadow-[inset_0_0_0_1.5px_var(--ink)]">
    {t.projects.switcher.isDefault}
  </span>
)

// 项目设置（交接说明第 11.3 节）：单栏，三个区块，没有分区目录。只在项目中有，平台没有设置页。
export function ProjectSettingsPage() {
  const scope = useScope()
  if (scope.kind !== 'project') return null
  const project = scope.project
  return (
    <DraftNavigationGuard key={project.id} projectName={project.name}><div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-[860px] flex-col gap-6 px-4 pt-[30px] pb-[72px] sm:px-[30px]">
        <div className="flex flex-col gap-1.5">
          <span className="inline-flex items-center gap-2 text-[13.5px] font-black text-ink-700">
            <SettingsIcon className="size-[15px]" strokeWidth={2.4} />
            {t.projects.settings.title}
            {project.isDefault && <DefaultTag />}
          </span>
          <h1 className={cn(pageTitleClass, 'mt-1 self-start')}>
            {project.name}
          </h1>
        </div>
        {/* 按项目 ID 重新挂载：输入框与提示不会带到另一个项目；改名后仍是同一个 ID，“已保存”保留。 */}
        <NameCard key={`name-${project.id}`} project={project} />
        <Card title={t.prompts.projectTitle}><ProjectPromptForm projectId={project.id} /></Card>
        <ContextFolderSection key={`folder-${project.id}`} scope={{ kind: 'project', projectId: project.id }} />
        <Card title={t.plugins.project.settingsCard}><p>{t.plugins.project.settingsCardBody}</p><Link className="font-bold text-riso-blue underline" to={`/p/${encodeURIComponent(project.name)}/plugins`}>{t.plugins.project.settingsLink}</Link></Card>
        <ManagerCard key={`manager-${project.id}`} project={project} scope={scope} />
        <ProjectCredentialSettings key={`accounts-${project.id}`} projectId={project.id} />
        {project.isDefault ? (
          <Card title={t.projects.settings.danger.title} tone="neutral">
            <p className="text-[14.5px]">{t.projects.settings.danger.defaultNote}</p>
          </Card>
        ) : (
          <DangerCard key={`danger-${project.id}`} project={project} />
        )}
      </div>
    </div></DraftNavigationGuard>
  )
}

// 改名：本地按命名规则预检；保存后项目列表即是新名称，地址由 ProjectScopeRoute 按 ID 换成新名称（不贴“在别处改名”的便签）。
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
    <Card title={t.projects.settings.name.title}>
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
              <p role="alert" className="text-[12.5px] leading-snug font-bold text-red-text">
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
    </Card>
  )
}

const statusBadge = (agent: Schemas['AgentListItem']) =>
  agent.sessionState === 'missing' ? (
    <Badge tone="danger">{t.agents.sessionMissing}</Badge>
  ) : agent.busy ? (
    <Badge tone="busy">{t.agents.busy}</Badge>
  ) : (
    <Badge tone="success">{t.agents.idle}</Badge>
  )

// Manager：从本项目的 Agent 中指定、更换或取消；立即生效，不弹确认框，结果用绿色提示。Agent 正忙时也可以指定。
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
    <Card title={t.projects.settings.manager.title}>
      <p className="text-[14px] text-ink-700">{t.projects.settings.manager.intro}</p>
      {manager ? (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-3 rounded-card bg-white px-4 py-3.5 shadow-print-rest">
          <AgentAvatar id={manager.id} />
          <span className="flex min-w-0 flex-[1_1_220px] flex-col">
            <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
              <Link
                to={agentPath(scope, manager.id, 'chat')}
                className="text-[16px] font-black text-ink underline decoration-riso-pink decoration-2 underline-offset-4"
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
        <div role="status" className="flex items-center gap-2.5 rounded-card bg-tint-blue px-4 py-3 text-[14.5px] font-black">
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
          <span id={labelId} className="text-[13.5px] leading-snug font-black">
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
    </Card>
  )
}

const busyAgentsOf = (error: unknown): { id: string; name: string }[] | null =>
  error instanceof ApiError && error.code === 'agent_not_idle' && error.details && 'agents' in error.details ? error.details.agents : null

// 删除项目：四条说明与删除按钮；确认框说明将删除的 Agent 数，焦点默认在“取消”上。
// 服务端因 Agent 不空闲拒绝时关闭确认框，在本卡片列出这些 Agent，保留到下一次点删除或离开页面；成功后进入默认项目。
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
    <Card title={t.projects.settings.danger.title} tone="danger">
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
      {activeTasks && <div role="alert" className="grid gap-2 rounded-card bg-white p-4 text-[14px]">
        <p className="font-black text-red-text">{credentialText.projectSettingsPageText1}</p>
        <ul className="grid gap-2">{activeTasks.map(task => <li key={task.id}><Link className="inline-flex min-h-9 items-center font-bold underline [overflow-wrap:anywhere]" to={`${tasksPath(project.name)}/${task.id}`}>{credentialText.projectSettingsPageText2}{task.id} · {task.status}</Link></li>)}</ul>
      </div>}
      {busy && (
        <div role="alert" className="flex flex-col gap-2.5 rounded-card bg-white px-[18px] py-4 shadow-[inset_0_0_0_2px_var(--red)]">
          <span className="text-[15px] font-black text-red-text">{t.projects.settings.danger.notIdleTitle}</span>
          <span className="text-[14px] text-ink-700">{t.projects.settings.danger.notIdleBody}</span>
          <ul aria-label={t.projects.settings.danger.busyList} className="flex flex-col gap-2">
            {busy.map((agent) => (
              <li key={agent.id} className="flex items-center gap-3 rounded-ticket bg-error-field px-3 py-2">
                <AgentAvatar id={agent.id} />
                <span className="min-w-0 flex-1 truncate text-[14.5px] font-black">{agent.name}</span>
                <Link
                  to={agentPath(scope, agent.id, 'chat')}
                  aria-label={t.projects.settings.danger.openChatOf(agent.name)}
                  className="inline-flex min-h-9 shrink-0 items-center gap-1.5 text-[13.5px] font-black text-ink underline decoration-riso-pink decoration-2 underline-offset-4"
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
    </Card>
  )
}
