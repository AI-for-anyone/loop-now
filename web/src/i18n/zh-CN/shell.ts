// Left sidebar and main-area page header. There are only two kinds of scope, “全局”
// (Global) and individual projects; platform-held Agents belong under Global.
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
  // Running count next to the “任务” (Tasks) item: the number of tasks with status
  // running; this sentence is used by screen readers and the hover tooltip.
  runningTasks: (count: number) => `${count} 个任务正在运行`,
  groups: {
    agents: 'Agent',
    projects: '项目',
    platformAgents: '平台 Agent',
  },
  chips: {
    busy: '回复中',
  },
  noProjects: '还没有项目',
  menu: '打开导航',
  closeMenu: '关闭导航',
  resizeSidebar: '调整侧边栏宽度',
  // Location trail in the main-area page header: project › Agent › name.
  breadcrumb: '页面位置',
  agentCrumb: 'Agent',
  platformAgentCrumb: '平台 Agent',
} as const
