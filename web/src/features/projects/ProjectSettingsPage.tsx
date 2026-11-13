import { useId, useState, type FormEvent, type ReactNode } from 'react'
import { Link, useNavigate } from 'react-router'
import { ArrowRightIcon, PlusIcon } from 'lucide-react'
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
import { agentPath, agentsPath, overviewPath, projectSettingsPath, type Scope } from '@/lib/scope'
import { useScope } from './scope-context'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { ProjectPromptForm } from '@/features/prompts/ProjectPromptForm'
import { ProjectCredentialSettings } from '@/features/project-credentials/ProjectAccounts'
import { tasksPath } from '@/api/tasks'
import { ContextFolderBody } from '@/features/folders/FolderSections'
import { ProjectLocalDirectories } from '@/features/local-mode/LocalDirectories'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { Sheet } from '@/components/Sheet'
import { SectionToc, SheetSection } from '@/components/SheetSections'

// 项目设置的编号分区，顺序即目录尺的顺序（改版：与 Agent 配置页同一套，排在一张纸上，右侧票根里是目录尺）。
const KEYS = ['name', 'prompt', 'folder', 'local-dirs', 'plugins', 'manager', 'accounts', 'danger'] as const
type Key = (typeof KEYS)[number]
const partId = (key: Key) => `settings-${key}`
const partTitleId = (key: Key) => `settings-${key}-title`
const titles: Record<Key, string> = {
  name: t.projects.settings.name.title,
  prompt: t.prompts.projectTitle,
  folder: t.folders.project.title,
  'local-dirs': t.localMode.directories.title,
  plugins: t.plugins.project.settingsCard,
  manager: t.projects.settings.manager.title,
  accounts: t.credentials.accounts.title,
  danger: t.projects.settings.danger.title,
}

// 一个编号分区。danger：删除项目一区，编号、标题与目录项红色，内容在红色斜纹的底上（默认项目不能删除，不用红色）。
function Part({ id, tone = 'default', children }: { id: Key; tone?: 'default' | 'danger'; children: ReactNode }) {
  return (
    <SheetSection id={partId(id)} titleId={partTitleId(id)} index={KEYS.indexOf(id)} title={titles[id]} tone={tone}>
      {children}
    </SheetSection>
  )
}

// 项目设置（交接说明第 11.3 节）：只在项目中有，平台没有设置页。页头是共用的页面标题（面包屑是项目名称，
// 旁边是默认项目标记与项目 ID）；八个分区排在一张纸上，右侧票根里是目录尺，窄屏不显示目录。
export function ProjectSettingsPage() {
  const scope = useScope()
  if (scope.kind !== 'project') return null
  const project = scope.project
  const toc = KEYS.map((key) => ({ id: partId(key), titleId: partTitleId(key), title: titles[key], danger: key === 'danger' && !project.isDefault }))
  return (
    <DraftNavigationGuard key={project.id} projectName={project.name}>
      <PageFrame>
        <PageHeader crumbs={[{ label: project.name, to: overviewPath({ kind: 'project', name: project.name }) }]} title={t.projects.settings.title}>
          {project.isDefault && <span className="shrink-0 rounded-full bg-ink px-2.5 text-[12px] leading-[22px] font-black text-white">{t.projects.settings.defaultBadge}</span>}
          <span className="min-w-0 font-mono text-[12px] [overflow-wrap:anywhere] text-ink-700">{project.id}</span>
        </PageHeader>
        <Sheet className="mt-2 flex-1">
          <div className="flex min-h-0 flex-1">
            {/* 滚动区是定位容器：里面绝对定位的元素（如 sr-only 说明）随内容滚动，不会按未滚动时的位置把整页撑高。 */}
            <div className="relative h-full min-w-0 flex-1 overflow-y-auto">
              <div className="flex max-w-[960px] flex-col gap-6 pt-[26px] pr-[clamp(14px,3vw,40px)] pb-16 pl-[clamp(12px,2vw,30px)]">
                {/* 按项目 ID 重新挂载：输入框与提示不会带到另一个项目；改名后仍是同一个 ID，“已保存”保留。 */}
                <NameCard key={`name-${project.id}`} project={project} />
                <Part id="prompt">
                  <ProjectPromptForm projectId={project.id} />
                </Part>
                <Part id="folder">
                  <ContextFolderBody key={`folder-${project.id}`} projectId={project.id} />
                </Part>
                <Part id="local-dirs">
                  <ProjectLocalDirectories key={`local-dirs-${project.id}`} projectId={project.id} />
                </Part>
                <Part id="plugins">
                  <p>{t.plugins.project.settingsCardBody}</p>
                  <Link className="self-start font-bold text-riso-blue underline" to={`/p/${encodeURIComponent(project.name)}/plugins`}>
                    {t.plugins.project.settingsLink}
                  </Link>
                </Part>
                <ManagerCard key={`manager-${project.id}`} project={project} scope={scope} />
                <Part id="accounts">
                  <ProjectCredentialSettings key={`accounts-${project.id}`} projectId={project.id} embedded />
                </Part>
                {project.isDefault ? (
                  <Part id="danger">
                    <p className="text-[14.5px]">{t.projects.settings.danger.defaultNote}</p>
                  </Part>
                ) : (
                  <DangerCard key={`danger-${project.id}`} project={project} />
                )}
              </div>
            </div>
            <SectionToc label={t.config.toc.title} items={toc} />
          </div>
        </Sheet>
      </PageFrame>
    </DraftNavigationGuard>
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
    <Part id="name">
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
    </Part>
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
    <Part id="manager">
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
    </Part>
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
    <Part id="danger" tone="danger">
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
        <p className="font-black text-red-text">{t.credentials.projectSettings.deleteBlocked}</p>
        <ul className="grid gap-2">{activeTasks.map(task => <li key={task.id}><Link className="inline-flex min-h-9 items-center font-bold underline [overflow-wrap:anywhere]" to={`${tasksPath(project.name)}/${task.id}`}>{t.credentials.projectSettings.viewTaskPrefix}{task.id} · {task.status}</Link></li>)}</ul>
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
    </Part>
  )
}
