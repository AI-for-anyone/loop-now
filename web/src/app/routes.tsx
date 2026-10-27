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
import { AgentFolderTab, ProjectFolderPage } from '@/features/folders/FolderSections'
import { NewSkillSheet, SkillDetailSheet, SkillsPage } from '@/features/skills-library/SkillsPage'
import { StatsPage } from '@/features/stats/StatsPage'
import { AppLayout } from './AppLayout'

// 项目与平台共用的 Agent 页：所选 Agent 的对话、配置、文件夹与收件箱（Agent 列表在左侧栏里）。
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
  {
    path: '/',
    element: <AppLayout />,
    children: [
      // 根地址是概览页：不带参数是全局，?project=<项目名> 是这个项目。
      { index: true, element: <OverviewPage /> },
      // 当前项目体现在地址中：项目在 /p/<项目名> 下，平台持有的 Agent 在 /platform 下（左侧栏归在全局之下）。
      {
        path: 'p/:projectName',
        element: <ProjectScopeRoute />,
        children: [{ index: true, element: <ScopeIndexRedirect /> }, agentsRoute(), { path: 'folder', element: <ProjectFolderPage /> }, { path: 'plugins', element: <ProjectPluginsPage /> }, { path: 'settings', element: <ProjectSettingsPage /> }, { path: 'tasks', element: <TasksPage /> }, { path: 'tasks/:taskId', element: <TaskDetail /> }],
      },
      {
        path: 'platform',
        element: <PlatformScopeRoute />,
        children: [{ index: true, element: <ScopeIndexRedirect /> }, agentsRoute()],
      },
      // 不带项目的旧地址：改写到最近使用的项目或平台，或 Agent 实际所在的地址。
      { path: 'agents', element: <LegacyAgentsRedirect /> },
      { path: 'tasks', element: <TasksEntry /> },
      { path: 'agents/:agentId/*', element: <LegacyAgentRedirect /> },
      {
        path: 'skills',
        element: <SkillsPage />,
        // 详情与在线新建是叠在模板网格上的抽屉，由地址控制开合。
        children: [
          { path: 'new', element: <NewSkillSheet /> },
          { path: ':skillId', element: <SkillDetailSheet /> },
        ],
      },
      // 收件箱页已去掉：Agent 发给你的消息在它的对话里，外部身份的在它的页面上。旧地址按线索转过去。
      { path: 'inbox/:threadId?', element: <InboxRedirect /> },
      // 外部身份属于平台，不在任何项目之下。
      { path: 'identities/:identityId?', element: <IdentitiesPage /> },
      { path: 'plugins', element: <PluginsPage /> },
      // 插件详情：概览之外的页签接在后面（/plugins/<id>/config 等）。
      { path: 'plugins/:pluginId/:tab?', element: <PluginDetailPage /> },
      // 统计页与概览一样可按全部、项目或平台筛选（写在地址里），左侧栏沿用之前的范围。
      { path: 'stats', element: <StatsPage /> },
      { path: 'settings', element: <SettingsPage /> },
      { path: '*', element: <Navigate to="/" replace /> },
    ],
  },
]
