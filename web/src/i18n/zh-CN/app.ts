export const app = {
  title: 'Loop Now 控制台',
  brand: 'LOOP NOW',
  // 左侧栏底部的连接状态；区域名称复用 settings.health，部分依赖不可用复用 settings.healthDegraded。
  topbar: { connected: '已连接', unreachable: '无法连接', checking: '检查中…' },
} as const

export const connection = {
  // 与各处请求失败时的“无法连接服务端”区分，便于同时出现时辨认全局提示。
  unreachableTitle: '无法连接到服务端',
  unreachableBody: (apiBase: string, origin: string) =>
    `控制台访问的服务端地址是 ${apiBase}。可能的原因：服务端没有启动；或者当前控制台地址 ${origin} 不在服务端允许的来源中（LOOP_NOW_CONSOLE_ORIGINS）。`,
} as const
