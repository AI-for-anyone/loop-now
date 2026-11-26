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

// The Agent's Folder tab: its one and only folder, both its working directory and its own context. Rendered as a full-bleed
// 2-pane explorer on the tab's sheet. On narrow screens it uses a bounded height and scrolls with the page; from the nav breakpoint
// upward it fills the tab's remaining height (same as the Chat tab).
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
      className="h-[clamp(28rem,80dvh,56rem)] nav:h-full"
    />
  )
}

// Shared folder embedded inside project settings and global settings: one sentence of explanation plus a compact 2-pane file manager
// (no Pull from Git).
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

// The page opened by Project folder in the sidebar: the folder shared by the Agents in the project. The header is the shared page
// title (breadcrumbs, title and description); the folder fills the sheet below in the same 2-pane explorer layout.
export function ProjectFolderPage() {
  const scope = useScope()
  const context = useContextInfo()
  const project = scope.kind === 'project' ? scope.project : null
  const projectId = project?.id ?? null
  const source = useMemo(() => (projectId ? folderSource({ kind: 'project', projectId }) : null), [projectId])
  if (!source || !project) return null
  return (
    <PageFrame className="nav:overflow-hidden">
      <PageHeader crumbs={[{ label: project.name, to: overviewPath({ kind: 'project', name: project.name }) }]} title={t.folders.project.title}>
        <span>{t.folders.project.intro}</span>
      </PageHeader>
      <Sheet className="h-[clamp(28rem,80dvh,56rem)] shrink-0 nav:h-auto nav:min-h-0 nav:flex-1 nav:shrink [[data-skin=plate]_&]:mt-2">
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
