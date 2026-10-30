// 左侧栏与主区页头。范围只有“全局”与各个项目两种，平台持有的 Agent 归在全局之下。
export const shell = {
  nav: '主导航',
  global: '全局',
  scope: {
    label: (name: string) => `切换范围，当前：${name}`,
    list: '范围',
    filter: '按名称过滤项目',
    projects: (count: number) => `项目 · ${count}`,
    noMatch: '没有匹配的项目',
    create: '新建项目',
  },
  search: {
    project: '搜索 Agent',
    global: '搜索项目与 Agent',
    placeholder: '搜索',
    shortcut: '⌘K',
  },
  items: {
    overview: '概览',
    tasks: '任务',
    folder: '项目文件夹',
    projectSettings: '项目设置',
    plugins: '插件市场',
    skills: 'Skills 库',
    stats: '统计',
    identities: '外部身份',
    settings: '全局设置',
  },
  groups: {
    agents: 'Agent',
    projects: '项目',
    platformAgents: '平台 Agent',
  },
  chips: {
    busy: '回复中',
    sleep: 'zZ sleep',
  },
  noProjects: '还没有项目',
  menu: '打开导航',
  closeMenu: '关闭导航',
  // 主区页头的位置链：项目 › Agent › 名称。
  breadcrumb: '页面位置',
  agentCrumb: 'Agent',
  platformAgentCrumb: '平台 Agent',
} as const
