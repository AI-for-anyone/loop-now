import type { components } from '@/api/schema.gen'

type S = components['schemas']

export const settings = {
  title: '全局设置',
  // 界面语言只保存在这个浏览器里，不是服务端配置。
  language: { title: '界面语言', hint: '只影响这个浏览器；切换后页面会重新加载。' },
  readonlyHint: '服务端的配置来自它的环境变量，这里只显示，控制台不能修改。',
  organization: '默认组织',
  project: '默认项目',
  server: '服务端',
  apiBase: 'API 地址',
  health: '连接状态',
  healthOk: '正常',
  healthDegraded: '部分依赖不可用',
  postgres: 'PostgreSQL',
  redis: 'Redis',
  dependency: (name: string, state: string) => `${name}：${state}`,
  cliTemplates: 'CLI 模板',
  version: '版本',
  install: {
    not_installed: '未安装',
    installing: '安装中',
    installed: '已安装',
    failed: '安装失败',
    unsupported_platform: '不支持当前平台',
  } satisfies Record<S['CliTemplate']['install']['status'], string>,
} as const
