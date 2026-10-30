import type { Messages } from '..'

export const settings = {
  title: 'Global settings',
  readonlyHint: "The server's config comes from its environment variables. It's shown here read-only; the console can't change it.",
  organization: 'Default organization',
  project: 'Default project',
  server: 'Server',
  apiBase: 'API address',
  health: 'Connection',
  healthOk: 'OK',
  healthDegraded: 'Some dependencies unavailable',
  postgres: 'PostgreSQL',
  redis: 'Redis',
  dependency: (name: string, state: string) => `${name}: ${state}`,
  cliTemplates: 'CLI templates',
  version: 'Version',
  install: {
    not_installed: 'Not installed',
    installing: 'Installing',
    installed: 'Installed',
    failed: 'Install failed',
    unsupported_platform: 'Not supported on this platform',
  },
} satisfies Messages['settings']
