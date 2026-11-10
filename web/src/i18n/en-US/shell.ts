import type { Messages } from '..'
import { plural } from '../format'

export const shell = {
  nav: 'Main navigation',
  global: 'Global',
  scope: {
    label: (name: string) => `Switch scope, current: ${name}`,
    list: 'Scopes',
    filter: 'Filter projects by name',
    projects: (count: number) => `Projects · ${count}`,
    noMatch: 'No matching projects',
    create: 'New project',
  },
  search: {
    project: 'Search Agents',
    global: 'Search projects and Agents',
    placeholder: 'Search',
    shortcut: '⌘K',
  },
  items: {
    overview: 'Overview',
    tasks: 'Tasks',
    folder: 'Project folder',
    projectSettings: 'Project settings',
    plugins: 'Plugin market',
    skills: 'Skills library',
    stats: 'Stats',
    identities: 'External identities',
    settings: 'Global settings',
  },
  runningTasks: (count: number) => `${count} ${plural('en-US', count, { one: 'task', other: 'tasks' })} running`,
  groups: {
    agents: 'Agents',
    projects: 'Projects',
    platformAgents: 'Platform Agents',
  },
  chips: {
    busy: 'Replying',
    sleep: 'zZ sleep',
  },
  noProjects: 'No projects yet',
  menu: 'Open navigation',
  closeMenu: 'Close navigation',
  breadcrumb: 'Breadcrumb',
  agentCrumb: 'Agent',
  platformAgentCrumb: 'Platform Agent',
} satisfies Messages['shell']
