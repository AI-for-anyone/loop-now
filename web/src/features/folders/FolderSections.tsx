import { useMemo } from 'react'
import { folderSource } from '@/api/folders'
import { useContextInfo } from '@/api/queries'
import { useAgentContext } from '@/features/agents/agent-context'
import { useScope } from '@/features/projects/scope-context'
import { Sheet } from '@/components/Sheet'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { overviewPath } from '@/lib/scope'
import { t } from '@/i18n'
import { FileManager } from './FileManager'

// Agent 的“文件夹”页签：它唯一的文件夹，既是工作目录，也是它自己的上下文。直接排在页签的纸上（右侧票根里是概况），
// 说明放在底部。窄屏时给定高度、随整页滚动；宽屏时占满页签剩下的高度（与对话页签相同）。
export function AgentFolderTab() {
  const { agent } = useAgentContext()
  const context = useContextInfo()
  const source = useMemo(() => folderSource({ kind: 'agent', agentId: agent.id }), [agent.id])
  return (
    <FileManager
      variant="sheet"
      source={source}
      rootLabel={t.folders.rootLabel.agent(agent.name)}
      note={t.folders.agent.intro(agent.name)}
      limits={context.data?.limits.textEdit}
      maxUpload={context.data?.limits.maxFolderUpload}
      className="h-[clamp(28rem,80dvh,56rem)] lg:h-full"
    />
  )
}

// 项目设置与全局设置里的共享文件夹：占满主栏宽度，高度随视口；没有“从 Git 拉取”。
// 项目设置里“项目文件夹”、全局设置里“全局文件夹”一区的内容：一句说明加一个文件管理器
// （改版：纸上分区里 2px 藏青描边的一块，与文件夹页同样的工具条与列表，没有票根）。
export function ContextFolderBody({ scope }: { scope: { kind: 'project'; projectId: string } | { kind: 'global' } }) {
  const context = useContextInfo()
  const source = useMemo(() => folderSource(scope), [scope.kind === 'project' ? scope.projectId : 'global']) // eslint-disable-line react-hooks/exhaustive-deps
  const copy = t.folders[scope.kind]
  return (
    <>
      <p className="text-[13.5px] text-ink-700">{copy.intro}</p>
      <FileManager
        variant="panel"
        source={source}
        rootLabel={t.folders.rootLabel[scope.kind]}
        limits={context.data?.limits.textEdit}
        maxUpload={context.data?.limits.maxFolderUpload}
        className="h-[clamp(30rem,70dvh,50rem)]"
      />
    </>
  )
}

// 左侧栏“项目文件夹”打开的页面：项目里的 Agent 共用的文件夹。页头是共用的页面标题（面包屑、标题与说明），
// 文件夹在一张纸上，与 Agent 的文件夹页签同一种版式（右侧票根里是概况）。
export function ProjectFolderPage() {
  const scope = useScope()
  const context = useContextInfo()
  const project = scope.kind === 'project' ? scope.project : null
  const projectId = project?.id ?? null
  const source = useMemo(() => (projectId ? folderSource({ kind: 'project', projectId }) : null), [projectId])
  if (!source || !project) return null
  return (
    <PageFrame className="lg:overflow-hidden">
      <PageHeader crumbs={[{ label: project.name, to: overviewPath({ kind: 'project', name: project.name }) }]} title={t.folders.project.title}>
        <span>{t.folders.project.intro}</span>
      </PageHeader>
      <Sheet className="mt-2 h-[clamp(28rem,80dvh,56rem)] shrink-0 lg:h-auto lg:min-h-0 lg:flex-1 lg:shrink">
        <FileManager
          key={projectId}
          variant="sheet"
          source={source}
          rootLabel={t.folders.rootLabel.project}
          limits={context.data?.limits.textEdit}
          maxUpload={context.data?.limits.maxFolderUpload}
          className="h-full"
        />
      </Sheet>
    </PageFrame>
  )
}
