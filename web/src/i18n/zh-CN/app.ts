export const app = {
  title: 'Loop Now 控制台',
  brand: 'LOOP NOW',
  // Connection status at the bottom of the left sidebar; the section name reuses
  // settings.health, and database unavailable reuses settings.healthDegraded.
  topbar: { connected: '已连接', unreachable: '无法连接', checking: '检查中…' },
} as const

export const connection = {
  // Kept distinct from the “无法连接服务端” (Can't reach the server) shown when individual
  // requests fail, so the global notice can be told apart when both appear.
  unreachableTitle: '无法连接到服务端',
  unreachableBody: (apiBase: string, origin: string) =>
    `控制台访问的服务端地址是 ${apiBase}。可能的原因：服务端没有启动；或者当前控制台地址 ${origin} 不在服务端允许的来源中（LOOP_NOW_CONSOLE_ORIGINS）。`,
} as const
