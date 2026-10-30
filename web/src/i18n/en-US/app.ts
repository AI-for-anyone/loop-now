import type { Messages } from '..'

export const app = {
  title: 'Loop Now Console',
  brand: 'LOOP NOW',
  topbar: { connected: 'Connected', unreachable: 'Unreachable', checking: 'Checking…' },
} satisfies Messages['app']

export const connection = {
  unreachableTitle: "Can't reach the server",
  unreachableBody: (apiBase: string, origin: string) =>
    `The console talks to the server at ${apiBase}. Either the server isn't running, or this console's address ${origin} isn't one of the server's allowed origins (LOOP_NOW_CONSOLE_ORIGINS).`,
} satisfies Messages['connection']
