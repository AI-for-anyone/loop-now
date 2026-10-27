import { useId, useMemo } from 'react'
import { folderSource } from '@/api/folders'
import { useContextInfo } from '@/api/queries'
import { useAgentContext } from '@/features/agents/agent-context'
import { useScope } from '@/features/projects/scope-context'
import { pageTitleClass } from '@/lib/page-title'
import { t } from '@/i18n'
import { FileManager } from './FileManager'

// Agent 的“文件夹”页签：它唯一的文件夹，既是工作目录，也是它自己的上下文。占满页签的宽度与高度。
export function AgentFolderTab() {
  const { agent } = useAgentContext()
  const context = useContextInfo()
  const source = useMemo(() => folderSource({ kind: 'agent', agentId: agent.id }), [agent.id])
  return (
    // 窄屏时给定高度、随整页滚动；宽屏时占满页签剩下的高度（与对话页签相同）。
    <div className="flex h-full min-h-0 flex-col gap-3 px-6 pt-4 pb-6 lg:overflow-hidden">
      <p className="shrink-0 text-[13.5px] text-ink-700">{t.folders.agent.intro(agent.name)}</p>
      <FileManager
        source={source}
        rootLabel={t.folders.rootLabel.agent(agent.name)}
        limits={context.data?.limits.textEdit}
        maxUpload={context.data?.limits.maxFolderUpload}
        className="h-[clamp(28rem,80dvh,56rem)] shrink-0 lg:h-auto lg:min-h-0 lg:flex-1 lg:shrink"
      />
    </div>
  )
}

// 项目设置与全局设置里的共享文件夹：占满主栏宽度，高度随视口；没有“从 Git 拉取”。
export function ContextFolderSection({ scope }: { scope: { kind: 'project'; projectId: string } | { kind: 'global' } }) {
  const context = useContextInfo()
  const titleId = useId()
  const source = useMemo(() => folderSource(scope), [scope.kind === 'project' ? scope.projectId : 'global']) // eslint-disable-line react-hooks/exhaustive-deps
  const copy = t.folders[scope.kind]
  return (
    <section aria-labelledby={titleId} className="flex flex-col gap-3.5 rounded-panel bg-paper-raised px-6 py-[22px] text-ink shadow-print-rest">
      <h2 id={titleId} className="font-display-cn text-[30px] leading-[1.15] font-normal">{copy.title}</h2>
      <p className="-mt-1 text-[14px] text-ink-700">{copy.intro}</p>
      <FileManager source={source} rootLabel={t.folders.rootLabel[scope.kind]} limits={context.data?.limits.textEdit} maxUpload={context.data?.limits.maxFolderUpload} className="h-[clamp(30rem,75dvh,54rem)]" />
    </section>
  )
}

// 左侧栏“项目文件夹”打开的页面：项目里的 Agent 共用的文件夹，占满主区（与 Agent 的文件夹页签相同）。
export function ProjectFolderPage() {
  const scope = useScope()
  const context = useContextInfo()
  const projectId = scope.kind === 'project' ? scope.project.id : null
  const source = useMemo(() => (projectId ? folderSource({ kind: 'project', projectId }) : null), [projectId])
  if (!source) return null
  return (
    <div className="flex h-full min-h-0 flex-col gap-3 px-4 pt-[30px] pb-6 sm:px-8 lg:overflow-hidden">
      <h1 className={pageTitleClass}>{t.folders.project.title}</h1>
      <p className="shrink-0 text-[13.5px] text-ink-700">{t.folders.project.intro}</p>
      <FileManager
        key={projectId}
        source={source}
        rootLabel={t.folders.rootLabel.project}
        limits={context.data?.limits.textEdit}
        maxUpload={context.data?.limits.maxFolderUpload}
        className="h-[clamp(28rem,80dvh,56rem)] shrink-0 lg:h-auto lg:min-h-0 lg:flex-1 lg:shrink"
      />
    </div>
  )
}
