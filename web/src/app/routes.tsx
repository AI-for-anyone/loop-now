import { PluginsPage } from '@/features/plugins/PluginsPage'
import { ProjectPluginsPage } from '@/features/plugins/ProjectPluginsPage'
import { PluginDetailPage } from '@/features/plugins/PluginDetailPage'
import { TasksEntry } from '@/features/tasks/TasksEntry'
import { TasksPage } from '@/features/tasks/TasksPage'
import { TaskDetail } from '@/features/tasks/TaskDetail'
import { Navigate, type RouteObject } from 'react-router'
import { AgentDetail } from '@/features/agents/AgentDetail'
import { AgentsIndex, AgentsPage } from '@/features/agents/AgentsPage'
import { ConfigPage } from '@/features/agent-config/ConfigPage'
import { ChatPage } from '@/features/chat/ChatPage'
import { AgentInboxTab } from '@/features/inbox/AgentInboxTab'
import { InboxRedirect } from '@/features/console-inbox/InboxRedirect'
import { IdentitiesPage } from '@/features/identities/IdentitiesPage'
import { OverviewPage } from '@/features/overview/OverviewPage'
import { ProjectSettingsPage } from '@/features/projects/ProjectSettingsPage'
import {
  AgentIndexRedirect,
  LegacyAgentRedirect,
  LegacyAgentsRedirect,
  PlatformScopeRoute,
  ProjectScopeRoute,
  ScopeIndexRedirect,
} from '@/features/projects/ScopeRoute'
import { SettingsPage } from '@/features/settings/SettingsPage'
import { PairPage } from '@/features/devices/PairPage'
import { ImportPage } from '@/features/importing/ImportPage'
import { AgentFolderTab, ProjectFolderPage } from '@/features/folders/FolderSections'
import { NewSkillSheet, SkillDetailSheet, SkillsPage } from '@/features/skills-library/SkillsPage'
import { StatsPage } from '@/features/stats/StatsPage'
import { AppLayout } from './AppLayout'
import { RouteErrorBoundary } from './RouteErrorBoundary'

// The Agent page shared by projects and the platform: the selected Agent's conversation, config, folder and inbox (the Agent list is in
// the sidebar).
const agentsRoute = (): RouteObject => ({
  path: 'agents',
  element: <AgentsPage />,
  children: [
    { index: true, element: <AgentsIndex /> },
    {
      path: ':agentId',
      element: <AgentDetail />,
      children: [
        { index: true, element: <AgentIndexRedirect /> },
        { path: 'chat', element: <ChatPage /> },
        { path: 'config', element: <ConfigPage /> },
        { path: 'folder', element: <AgentFolderTab /> },
        { path: 'inbox', element: <AgentInboxTab /> },
      ],
    },
  ],
})

export const routes: RouteObject[] = [
  // Device pairing runs outside AppLayout so opening /pair#<code> on the remote door never fires authenticated console requests first.
  { path: '/pair', element: <PairPage />, errorElement: <RouteErrorBoundary /> },
  {
    path: '/',
    element: <AppLayout />,
    errorElement: <RouteErrorBoundary />,
    children: [
      // The root URL is the overview page: global without parameters, that project with ?project=<project name>.
      { index: true, element: <OverviewPage /> },
      // The current project is part of the URL: projects live under /p/<project name>, and Agents owned by the platform under /platform (filed
      // under global in the sidebar).
      {
        path: 'p/:projectName',
        element: <ProjectScopeRoute />,
        children: [{ index: true, element: <ScopeIndexRedirect /> }, agentsRoute(), { path: 'folder', element: <ProjectFolderPage /> }, { path: 'plugins', element: <ProjectPluginsPage /> }, { path: 'settings/:tab?', element: <ProjectSettingsPage /> }, { path: 'tasks', element: <TasksPage /> }, { path: 'tasks/:taskId', element: <TaskDetail /> }],
      },
      {
        path: 'platform',
        element: <PlatformScopeRoute />,
        children: [{ index: true, element: <ScopeIndexRedirect /> }, agentsRoute()],
      },
      // Old URLs without a project: rewrite to the last-used project or the platform, or to where the Agent actually lives.
      // Importing conversations from the CLIs on this computer (opens by itself on a fresh install with no Agents).
      { path: 'import', element: <ImportPage /> },
      { path: 'agents', element: <LegacyAgentsRedirect /> },
      { path: 'tasks', element: <TasksEntry /> },
      { path: 'agents/:agentId/*', element: <LegacyAgentRedirect /> },
      {
        path: 'skills',
        element: <SkillsPage />,
        // The details and in-place creation are drawers stacked on the template grid, opened and closed by the URL.
        children: [
          { path: 'new', element: <NewSkillSheet /> },
          { path: ':skillId', element: <SkillDetailSheet /> },
        ],
      },
      // The inbox page is gone: messages an Agent sends you are in its conversation, an external identity's are on its page. Old URLs are
      // forwarded by thread.
      { path: 'inbox/:threadId?', element: <InboxRedirect /> },
      // External identities belong to the platform, not to any project.
      { path: 'identities/:identityId?/:tab?', element: <IdentitiesPage /> },
      { path: 'plugins', element: <PluginsPage /> },
      // Plugin details: tabs other than the overview are appended to the path (/plugins/<id>/config and so on).
      { path: 'plugins/:pluginId/:tab?', element: <PluginDetailPage /> },
      // Like the overview, the stats page can be filtered to everything, a project or the platform (kept in the URL); the sidebar keeps the
      // previous scope.
      { path: 'stats', element: <StatsPage /> },
      { path: 'settings/:tab?', element: <SettingsPage /> },
      { path: '*', element: <Navigate to="/" replace /> },
    ],
  },
]
