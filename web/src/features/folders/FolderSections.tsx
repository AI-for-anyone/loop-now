import { useMemo } from 'react'
import { useSearchParams } from 'react-router'
import { folderSource } from '@/api/folders'
import { useContextInfo } from '@/api/queries'
import { useAgentContext } from '@/features/agents/agent-context'
import { useScope } from '@/features/projects/scope-context'
import { Sheet } from '@/components/Sheet'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { overviewPath } from '@/lib/scope'
import { t } from '@/i18n'
import { folderPath } from './folder-source'
import { FileManager } from './FileManager'

// The Agent's “文件夹” (Folder) tab: its one and only folder, both its working directory and its own context. Laid directly on the tab's
// sheet (with the overview in the stub on the right),
// with the note at the bottom. On narrow screens it gets a fixed height and scrolls with the whole page; on wide screens it fills the
// tab's remaining height (same as the Chat tab).
export function AgentFolderTab() {
  const { agent } = useAgentContext()
  const context = useContextInfo()
  const [searchParams] = useSearchParams()
  const initialPath = folderPath(searchParams.get('path') ?? '') || undefined
  const source = useMemo(() => folderSource({ kind: 'agent', agentId: agent.id }), [agent.id])
  return (
    <FileManager
      key={initialPath ?? ''}
      variant="sheet"
      source={source}
      initialPath={initialPath}
      rootLabel={t.folders.rootLabel.agent(agent.name)}
      note={t.folders.agent.intro(agent.name)}
      limits={context.data?.limits.textEdit}
      maxUpload={context.data?.limits.maxFolderUpload}
      className="h-[clamp(28rem,80dvh,56rem)] lg:h-full"
    />
  )
}

// The shared folder in project settings and global settings: fills the main column's width, height follows the viewport; no
// “从 Git 拉取” (Pull from Git).
// The content of the “项目文件夹” (Project folder) section in project settings and the “全局文件夹” (Global folder) section in global
// settings: one sentence of explanation plus a file manager
// (redesign: a block with a 2px navy outline inside a section of the sheet, with the same toolbar and list as the folder page, and no stub).
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

// The page opened by “项目文件夹” (Project folder) in the sidebar: the folder shared by the Agents in the project. The header is the
// shared page title (breadcrumbs, title and description);
// the folder sits on a sheet, in the same layout as the Agent's Folder tab (with the overview in the stub on the right).
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
      <Sheet className="h-[clamp(28rem,80dvh,56rem)] shrink-0 lg:h-auto lg:min-h-0 lg:flex-1 lg:shrink">
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
